package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
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
// только хэш. revoke закрывает вход, база человека остается; purge снимает
// человека целиком, с базой, файлами ящика и версиями объектов.
// usersSubs are the subcommands of users.
var usersSubs = []string{"add", "revoke", "purge", "scope"}

func runUsers(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprintln(stdout, usersUsage) // просьба о помощи — успех (help_test.go)
		return 0
	}
	if len(args) == 0 || !slices.Contains(usersSubs, args[0]) {
		fmt.Fprintln(stderr, usersUsage)
		return 2
	}
	sub := args[0]
	tg, code := usersTarget(sub, args[1:], stderr)
	if tg == nil {
		return code
	}
	switch sub {
	case "revoke":
		return usersRevoke(tg.store, tg.id, stderr)
	case "purge":
		return usersPurge(tg, stderr)
	case "scope":
		return usersScope(tg, stderr)
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
	scope    domain.Scope  // add: область токена
	confirm  string        // purge: имя еще раз
	yes      bool          // purge: предпросмотр показан, удалять
	wait     time.Duration // purge: пауза между отзывом и удалением
}

// usersOpenStore opens the people's store; тесты подменяют хранилище, которое
// записывает и отвечает ошибкой.
var usersOpenStore = openTenantStore

// usersPurgeSleep waits for other server instances to learn of a revoke; тесты
// подменяют.
var usersPurgeSleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

const usersUsage = "usage: kbengine users add|revoke|purge|scope --tenants <s3://bucket | dir> --id <name> [add: --token-out <new file> --scope read|write] [scope: --scope read|write] [purge: --confirm <name> --wait <duration> --yes]"

// usersFlags are the flags specific to one subcommand.
type usersFlags struct {
	scope, tokenOut, confirm string
	wait                     time.Duration
	yes                      bool
}

// apply checks the subcommand's own flags and fills tg; code 0 — все верно.
func (fl usersFlags) apply(sub string, tg *usersTargetOf, stderr io.Writer) int {
	switch sub {
	case "purge":
		if fl.confirm != tg.id.String() {
			fmt.Fprintf(stderr, "users purge: --confirm <имя> обязателен и равен --id: удаление необратимо\n")
			return 2
		}
		if fl.wait < 0 {
			fmt.Fprintf(stderr, "users purge: --wait не может быть отрицательным\n")
			return 2
		}
		tg.confirm, tg.wait, tg.yes = fl.confirm, fl.wait, fl.yes
	case "add", "scope":
		scope, err := domain.NewScope(fl.scope)
		if err != nil {
			fmt.Fprintf(stderr, "users %s: %v\n", sub, err)
			return 2
		}
		if sub == "scope" && fl.scope == "" {
			fmt.Fprintf(stderr, "users scope: --scope read|write обязателен\n")
			return 2
		}
		tg.scope = scope
		if sub == "add" {
			if fl.tokenOut == "" {
				fmt.Fprintf(stderr, "users add: --token-out <новый файл> обязателен: в терминал токен не выводится\n")
				return 2
			}
			tg.tokenOut = fl.tokenOut
		}
	}
	return 0
}

// usersTarget parses the flags into the store and the person; nil — выйти с
// кодом code.
func usersTarget(sub string, args []string, stderr io.Writer) (*usersTargetOf, int) {
	fs := flag.NewFlagSet("users "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	loc := fs.String("tenants", "", "s3://bucket or directory, the same as serve --tenants")
	s3TokenFile := fs.String("s3-token-file", "", "optional file with an IAM token for s3:// (mode 0600)")
	name := fs.String("id", "", "person's name: a-z, 0-9 and inner hyphens, up to 32")
	var fl usersFlags
	if sub == "add" || sub == "scope" {
		fs.StringVar(&fl.scope, "scope", "", "read: the token only reads; write: as before")
	}
	if sub == "add" {
		fs.StringVar(&fl.tokenOut, "token-out", "", "write the token into this new file (mode 0600; an existing file is refused)")
	}
	if sub == "purge" {
		fs.BoolVar(&fl.yes, "yes", false, "remove: without it the command only shows what exists and changes nothing")
		fs.StringVar(&fl.confirm, "confirm", "", "the same name again: the removal cannot be undone")
		fs.DurationVar(&fl.wait, "wait", userauth.MaxStale+10*time.Second, "pause between the revoke and the removal, so every server instance stops admitting the token (a failed registry read keeps the old list for MaxStale)")
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
	if code := fl.apply(sub, tg, stderr); code != 0 {
		return nil, code
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
		// Область называется явно и при write: прежняя запись об этом имени (после
		// отзыва) не делает новый токен ограниченным молча. Токен у вызывающего
		// появляется только после этой записи; не вышло — выпуск откатывается.
		err = userauth.SetScope(ctx, tg.store, tg.id, tg.scope)
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

// usersPurge closes the person's login, waits for the other server instances to
// notice (userauth.RefreshEvery), then removes the base, the mailbox files and
// every version of them. Прерывание в паузе оставляет данные целыми и вход
// закрытым: повтор команды доводит дело.
func usersPurge(tg *usersTargetOf, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if _, ok := tg.store.(blob.Purger); !ok {
		fmt.Fprintf(stderr, "users purge: хранилище не умеет удалять дерево с версиями: ничего не изменено\n")
		return 1
	}
	if !tg.yes {
		return usersPurgePreview(ctx, tg, stderr)
	}
	registered := true
	if err := userauth.Revoke(ctx, tg.store, tg.id); errors.Is(err, domain.ErrNoSuchUser) {
		registered = false
	} else if err != nil {
		fmt.Fprintf(stderr, "users purge: %v\n", err)
		return 1
	} else {
		fmt.Fprintf(stderr, "users purge: вход %s закрыт\n", tg.id)
	}
	if tg.wait > 0 { // и при повторе: отозвать второй раз нечем, но экземпляры могли еще не узнать
		fmt.Fprintf(stderr, "users purge: жду %s, пока все экземпляры сервера узнают об отзыве\n", tg.wait)
		if err := usersPurgeSleep(ctx, tg.wait); err != nil {
			fmt.Fprintf(stderr, "users purge: прервано (%v): вход закрыт, данные целы; повтори kbengine users purge --tenants %s --id %s --confirm %s\n", err, shellQuote(tg.loc), tg.id, tg.id)
			return 1
		}
	}
	rep, err := userauth.Purge(ctx, tg.store, tg.id)
	if err != nil {
		fmt.Fprintf(stderr, "users purge: %v; уже удалено: база %d, файлы ящика %d, входы телефонов %d; повтор безопасен\n", err, rep.Base, rep.Mail, rep.Seen)
		return 1
	}
	// Второй проход: запрос, впущенный до того, как экземпляр узнал об отзыве,
	// мог записать уже после удаления.
	if late, err := userauth.Purge(ctx, tg.store, tg.id); err != nil {
		fmt.Fprintf(stderr, "users purge: проверка после удаления: %v; повтори команду\n", err)
		return 1
	} else if late != (userauth.PurgeReport{}) {
		fmt.Fprintf(stderr, "users purge: после удаления появились записи (база %d, ящик %d, входы %d), они убраны; отчет ниже неполон, повтори команду еще раз и проверь, что список пуст\n", late.Base, late.Mail, late.Seen)
		return 1
	}
	if !registered && rep == (userauth.PurgeReport{}) {
		fmt.Fprintf(stderr, "users purge: %s нет ни в реестре, ни в хранилище: опечатка в имени?\n", tg.id)
		return 1
	}
	fmt.Fprintf(stderr, "users purge: %s удален: база %d, файлы ящика %d, входы телефонов %d (объекты со всеми версиями)\n", tg.id, rep.Base, rep.Mail, rep.Seen)
	fmt.Fprintf(stderr, "users purge: логи сервера в Monium (ошибки, без тел запросов и токенов) не удаляются, они живут по сроку хранения Monium\n")
	fmt.Fprintf(stderr, "users purge: имя и хэш токена еще 30 дней лежат в старых версиях общих реестров auth/*.json: файлы общие, версии по одной не вычистить, не задев чужих\n")
	return 0
}

// usersPurgePreview shows what the name stands for and changes nothing: имя,
// набранное дважды верно, но не про того человека, не удаляет молча.
func usersPurgePreview(ctx context.Context, tg *usersTargetOf, stderr io.Writer) int {
	registered := userauth.NewRegistry(ctx, tg.store).Has(tg.id)
	phones := 0
	if ds, err := userauth.Devices(ctx, tg.store, tg.id); err == nil {
		phones = len(ds)
	}
	base := "есть"
	if _, err := tg.store.Get(ctx, userauth.CatalogKey(tg.id)); errors.Is(err, blob.ErrNotFound) {
		base = "нет"
	}
	reg := "нет"
	if registered {
		reg = "да"
	}
	fmt.Fprintf(stderr, "users purge: %s: в реестре: %s; телефонов: %d; база: %s\n", tg.id, reg, phones, base)
	fmt.Fprintf(stderr, "users purge: удалится без возврата: вход, телефоны, push, база и финансы, файлы ящика, входы телефонов, все версии объектов. Это тот человек? Повтори с --yes\n")
	return 2
}

// usersScope changes the scope of an existing person without a new token
// (#701): так облачный арендатор становится read-only. Сервер узнает за минуту.
func usersScope(tg *usersTargetOf, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !userauth.NewRegistry(ctx, tg.store).Has(tg.id) {
		fmt.Fprintf(stderr, "users scope: %v: %s\n", domain.ErrNoSuchUser, tg.id)
		return 1
	}
	if err := userauth.SetScope(ctx, tg.store, tg.id, tg.scope); err != nil {
		fmt.Fprintf(stderr, "users scope: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "users scope: %s: %s (сервер узнает за %s)\n", tg.id, tg.scope, userauth.RefreshEvery)
	return 0
}
