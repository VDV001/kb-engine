package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// runUsers is `kbengine users add|revoke --tenants <хранилище> --id <имя>`:
// люди сервера serve --tenants. add кладет токен в Связку ключей этого Мака
// (--keychain) или в новый файл 0600 (--token-out) и никогда в терминал: вывод
// терминала уходит в историю оболочки, журнал агента и переписку с моделью, а
// токен открывает базу и финансы человека. В хранилище только хэш. revoke
// закрывает вход, база человека остается.
func runUsers(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprintln(stdout, usersUsage) // просьба о помощи — успех (help_test.go)
		return 0
	}
	if len(args) == 0 || (args[0] != "add" && args[0] != "revoke") {
		fmt.Fprintln(stderr, usersUsage)
		return 2
	}
	sub := args[0]
	tg, code := usersTarget(sub, args[1:], stderr)
	if tg == nil {
		return code
	}
	if sub == "revoke" {
		return usersRevoke(tg.store, tg.id, stderr)
	}
	return usersAdd(tg, stderr)
}

// usersTargetOf is the store and the person a users command acts on, and for
// add — where the token goes: ровно одно из keychain и tokenOut.
type usersTargetOf struct {
	store    blob.Store
	loc      string // --tenants как назван: подсказки печатают готовую команду
	id       domain.UserID
	keychain string
	tokenOut string
}

// tokenKeychain is where users add --keychain puts the token: Связка ключей
// этого Мака, в тестах — память.
type tokenKeychain interface {
	Get(account string) (string, error)
	Set(account, secret string) error
	Delete(account string) error
}

// usersKeychain — та же служба, что читают окно и kbengine-mcp.
var usersKeychain = func() tokenKeychain { return keychain.Store{Service: "kb-desktop"} }

// usersOpenStore opens the people's store; тесты подменяют хранилище, которое
// записывает и отвечает ошибкой.
var usersOpenStore = openTenantStore

const usersUsage = "usage: kbengine users add|revoke --tenants <s3://bucket | dir> --id <name> [add: --keychain <source id> | --token-out <new file>]"

// usersTarget parses the flags into the store and the person; nil — выйти с
// кодом code.
func usersTarget(sub string, args []string, stderr io.Writer) (*usersTargetOf, int) {
	fs := flag.NewFlagSet("users "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	loc := fs.String("tenants", "", "s3://bucket or directory, the same as serve --tenants")
	s3TokenFile := fs.String("s3-token-file", "", "optional file with an IAM token for s3:// (mode 0600)")
	name := fs.String("id", "", "person's name: a-z, 0-9 and inner hyphens, up to 32")
	var kc, out *string
	if sub == "add" {
		// Только свой токен: службу kb-desktop читают окно и агент этого Мака,
		// и чужой токен в ней открыл бы им чужую базу. Чужому — --token-out.
		kc = fs.String("keychain", "", "put YOUR token into this Mac's Keychain under this account (a new source id); another person's token goes to --token-out")
		out = fs.String("token-out", "", "write the token into this new file (mode 0600; an existing file is refused)")
	}
	if code, stop := parseFlags(fs, args); stop {
		return nil, code
	}
	if *loc == "" {
		fmt.Fprintf(stderr, "users %s: --tenants is required\n%s\n", sub, usersUsage)
		return nil, 2
	}
	id, err := domain.NewUserID(*name)
	if err != nil {
		fmt.Fprintf(stderr, "users %s: %v\n", sub, err)
		return nil, 2
	}
	tg := &usersTargetOf{loc: *loc, id: id}
	if sub == "add" {
		if (*kc == "") == (*out == "") {
			fmt.Fprintf(stderr, "users add: назови ровно одно место для токена: --keychain <id источника> или --token-out <новый файл>; в терминал токен не выводится\n")
			return nil, 2
		}
		tg.keychain, tg.tokenOut = *kc, *out
	}
	store, err := usersOpenStore(*loc, *s3TokenFile)
	if err != nil {
		fmt.Fprintf(stderr, "users %s: %v\n", sub, err)
		return nil, 2
	}
	tg.store = store
	return tg, 0
}

func usersRevoke(store blob.Store, id domain.UserID, stderr io.Writer) int {
	if err := userauth.Revoke(context.Background(), store, id); err != nil {
		fmt.Fprintf(stderr, "users revoke: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "users revoke: вход %s закрыт (сервер узнает за %s); база осталась в хранилище\n", id, userauth.RefreshEvery)
	return 0
}

func usersAdd(tg *usersTargetOf, stderr io.Writer) int {
	ctx := context.Background()
	f, ok := claimPlace(tg, stderr)
	if !ok {
		return 2
	}
	dropFile := func() {
		if f == nil {
			return
		}
		// Удаляется только свой файл: путь мог за это время достаться другому.
		mine, err := f.Stat()
		_ = f.Close()
		if now, nerr := os.Stat(tg.tokenOut); err == nil && nerr == nil && os.SameFile(mine, now) {
			_ = os.Remove(tg.tokenOut)
		}
	}
	// От выпуска до сохранения прерывание оставило бы хэш без токена: Ctrl-C
	// здесь не действует. Ожидание Связки ограничено ее сроком (keychain).
	signal.Ignore(os.Interrupt, syscall.SIGTERM)
	defer signal.Reset(os.Interrupt, syscall.SIGTERM)
	tok, err := userauth.Add(ctx, tg.store, tg.id)
	if err != nil {
		dropFile()
		if errors.Is(err, domain.ErrUserExists) {
			fmt.Fprintf(stderr, "users add: %v — новый токен: сначала users revoke\n", err)
		} else {
			fmt.Fprintf(stderr, "users add: %v\n", err)
		}
		return 1
	}
	where, err := keepToken(tg, f, tok)
	if err != nil {
		// Токен нигде не сохранился, а хэш уже в хранилище: вход закрывается
		// сразу, иначе к нему нет ключа, а повторный add ответил бы «уже есть».
		// Текст отказа идет в терминал, а security мог повторить в нем строку
		// с секретом — токен из него вырезается.
		why := strings.ReplaceAll(err.Error(), tok, "…")
		dropFile()
		if rerr := userauth.Revoke(ctx, tg.store, tg.id); rerr != nil {
			fmt.Fprintf(stderr, "users add: токен не сохранен (%s), и вход не закрылся (%v): kbengine users revoke --tenants %s --id %s\n", why, rerr, tg.loc, tg.id)
			return 1
		}
		fmt.Fprintf(stderr, "users add: токен не сохранен (%s) — вход %s закрыт, повтори add\n", why, tg.id)
		return 1
	}
	fmt.Fprintf(stderr, "users add: токен %s %s; в хранилище только хэш\n", tg.id, where)
	return 0
}

// claimPlace checks the place for the token before it is issued: занятое или
// недоступное — отказ, пока токена еще нет. O_EXCL не дает затереть чужой
// файл, а занятая учетка Связки — токен источника, уже настроенного в окне
// (security -U заменил бы его молча). Для --token-out отдает открытый файл.
func claimPlace(tg *usersTargetOf, stderr io.Writer) (*os.File, bool) {
	if tg.tokenOut != "" {
		f, err := os.OpenFile(tg.tokenOut, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			fmt.Fprintf(stderr, "users add: %v\n", err)
			return nil, false
		}
		return f, true
	}
	_, err := usersKeychain().Get(tg.keychain)
	if err == nil {
		fmt.Fprintf(stderr, "users add: в Связке ключей уже есть токен под %s — назови новый id источника\n", tg.keychain)
		return nil, false
	}
	if !errors.Is(err, keychain.ErrNotFound) {
		fmt.Fprintf(stderr, "users add: Связка ключей недоступна (%v) — токен не выпущен\n", err)
		return nil, false
	}
	return nil, true
}

// keepToken puts tok where the flags said and names the place for the person.
func keepToken(tg *usersTargetOf, f *os.File, tok string) (string, error) {
	if f != nil {
		if _, err := f.WriteString(tok + "\n"); err != nil {
			return "", err
		}
		// Sync до «готово»: иначе сбой питания вернул бы пустой файл при
		// живом хэше.
		if err := f.Sync(); err != nil {
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		return "в файле " + tg.tokenOut + " (0600): передай его человеку и удали", nil
	}
	kc := usersKeychain()
	if err := kc.Set(tg.keychain, tok); err != nil {
		// Учетка была пуста (проверено до выпуска): все, что в ней теперь
		// есть, — этот токен, и после закрытия входа он мертвый.
		_ = kc.Delete(tg.keychain)
		return "", err
	}
	return "в Связке ключей (kb-desktop, " + tg.keychain + ")", nil
}
