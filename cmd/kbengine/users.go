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
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// runUsers is `kbengine users add|revoke --tenants <хранилище> --id <имя>`:
// люди сервера serve --tenants. add пишет токен только в новый файл 0600
// (--token-out) и никогда в терминал: вывод терминала уходит в историю
// оболочки, журнал агента и переписку с моделью, а токен открывает базу и
// финансы человека. Связку ключей ведет окно: токен попадает туда из файла
// (pbcopy < файл и вставить в окно), а не из командной строки. В хранилище
// только хэш. revoke закрывает вход, база человека остается.
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
// add — the new file the token goes to.
type usersTargetOf struct {
	store    blob.Store
	loc      string // --tenants как назван: подсказки печатают готовую команду
	id       domain.UserID
	tokenOut string
}

// usersOpenStore opens the people's store; тесты подменяют хранилище, которое
// записывает и отвечает ошибкой.
var usersOpenStore = openTenantStore

const usersUsage = "usage: kbengine users add|revoke --tenants <s3://bucket | dir> --id <name> [add: --token-out <new file>]"

// usersTarget parses the flags into the store and the person; nil — выйти с
// кодом code.
func usersTarget(sub string, args []string, stderr io.Writer) (*usersTargetOf, int) {
	fs := flag.NewFlagSet("users "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	loc := fs.String("tenants", "", "s3://bucket or directory, the same as serve --tenants")
	s3TokenFile := fs.String("s3-token-file", "", "optional file with an IAM token for s3:// (mode 0600)")
	name := fs.String("id", "", "person's name: a-z, 0-9 and inner hyphens, up to 32")
	var out *string
	if sub == "add" {
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
		if *out == "" {
			fmt.Fprintf(stderr, "users add: --token-out <новый файл> обязателен: в терминал токен не выводится\n")
			return nil, 2
		}
		tg.tokenOut = *out
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
	// Ctrl-C, закрытое окно терминала и kill отменяют выпуск, а не обрывают
	// его посередине: отмененный выпуск откатывается ниже. Ловятся с самого
	// начала — прерывание до выпуска не оставит пустого файла. Не покрыт
	// только kill -9.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	// Файл открывается до выпуска: занятое место — отказ, пока токена еще нет,
	// а O_EXCL не дает затереть чужой файл.
	f, err := os.OpenFile(tg.tokenOut, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fmt.Fprintf(stderr, "users add: %v\n", err)
		return 2
	}
	// Свой ли файл, решает то, что открыто сейчас: путь мог потом достаться
	// другому, а закрытый с ошибкой файл на Stat уже не ответит.
	mine, err := f.Stat()
	if err != nil {
		_ = f.Close()
		fmt.Fprintf(stderr, "users add: %v\n", err)
		return 1
	}
	dropFile := func() {
		_ = f.Close()
		if now, err := os.Stat(tg.tokenOut); err == nil && os.SameFile(mine, now) {
			_ = os.Remove(tg.tokenOut)
		}
	}
	tok, err := userauth.NewToken()
	if err != nil {
		dropFile()
		fmt.Fprintf(stderr, "users add: %v\n", err)
		return 1
	}
	err = userauth.AddToken(ctx, tg.store, tg.id, tok)
	if errors.Is(err, domain.ErrUserExists) {
		dropFile()
		fmt.Fprintf(stderr, "users add: %v — новый токен: сначала users revoke\n", err)
		return 1
	}
	if err == nil {
		err = keepToken(f, tok)
	}
	if err != nil {
		dropFile()
		stop() // откат не глушит Ctrl-C: второе прерывание — воля человека
		return undoAdd(tg, tok, err, stderr)
	}
	fmt.Fprintf(stderr, "users add: токен %s в файле %s (0600): pbcopy < %s, вставить в окно или передать человеку, потом удалить файл; в хранилище только хэш\n", tg.id, tg.tokenOut, shellQuote(tg.tokenOut))
	return 0
}

// undoAdd takes back tok when it was not kept: хэш без токена только
// заставил бы следующий add ответить «уже есть». Ответ хранилища мог
// потеряться после записи, поэтому откат пробуется и при ошибке самой
// записи. Снимается только запись этого токена: рабочий вход того же
// человека остается. ErrNoSuchUser — хэш этого токена так и не записался.
func undoAdd(tg *usersTargetOf, tok string, cause error, stderr io.Writer) int {
	// Свой срок: контекст выпуска мог отменить сигнал.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rerr := userauth.RevokeToken(ctx, tg.store, tg.id, tok)
	switch {
	case errors.Is(rerr, domain.ErrNoSuchUser):
		fmt.Fprintf(stderr, "users add: %v — токен не выпущен; повтори add\n", cause)
		return 1
	case rerr == nil:
		fmt.Fprintf(stderr, "users add: %v — вход %s закрыт: выпущенный токен отозван; повтори add\n", cause, tg.id)
		return 1
	}
	fmt.Fprintf(stderr, "users add: %v, и вход не закрылся (%v): kbengine users revoke --tenants %s --id %s\n", cause, rerr, shellQuote(tg.loc), tg.id)
	return 1
}

// keepToken writes tok into the opened file. Sync до «готово»: иначе сбой
// питания вернул бы пустой файл при живом хэше.
func keepToken(f *os.File, tok string) error {
	if _, err := f.WriteString(tok + "\n"); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// shellQuote quotes s for a command the person copies: путь с пробелом иначе
// развалил бы подсказку.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
