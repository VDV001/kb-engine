package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// runUsers is `kbengine users add|revoke --tenants <хранилище> --id <имя>`:
// люди сервера serve --tenants. add печатает токен в stdout один раз — больше
// его не покажет никто, в хранилище только хэш. revoke закрывает вход, база
// человека остается.
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
	return usersAdd(tg.store, tg.id, stdout, stderr)
}

// usersTargetOf is the store and the person a users command acts on.
type usersTargetOf struct {
	store blob.Store
	id    domain.UserID
}

const usersUsage = "usage: kbengine users add|revoke --tenants <s3://bucket | dir> --id <name>"

// usersTarget parses the flags into the store and the person; nil — выйти с
// кодом code.
func usersTarget(sub string, args []string, stderr io.Writer) (*usersTargetOf, int) {
	fs := flag.NewFlagSet("users "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	loc := fs.String("tenants", "", "s3://bucket or directory, the same as serve --tenants")
	s3TokenFile := fs.String("s3-token-file", "", "optional file with an IAM token for s3:// (mode 0600)")
	name := fs.String("id", "", "person's name: a-z, 0-9 and inner hyphens, up to 32")
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
	store, err := openTenantStore(*loc, *s3TokenFile)
	if err != nil {
		fmt.Fprintf(stderr, "users %s: %v\n", sub, err)
		return nil, 2
	}
	return &usersTargetOf{store: store, id: id}, 0
}

func usersRevoke(store blob.Store, id domain.UserID, stderr io.Writer) int {
	if err := userauth.Revoke(context.Background(), store, id); err != nil {
		fmt.Fprintf(stderr, "users revoke: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "users revoke: вход %s закрыт (сервер узнает за %s); база осталась в хранилище\n", id, userauth.RefreshEvery)
	return 0
}

func usersAdd(store blob.Store, id domain.UserID, stdout, stderr io.Writer) int {
	tok, err := userauth.Add(context.Background(), store, id)
	if errors.Is(err, domain.ErrUserExists) {
		fmt.Fprintf(stderr, "users add: %v — новый токен: сначала users revoke\n", err)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "users add: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "users add: токен %s показан один раз — сохрани его в Связке ключей, в хранилище только хэш\n", id)
	fmt.Fprintln(stdout, tok)
	return 0
}
