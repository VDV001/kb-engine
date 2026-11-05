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
	store, id, code := usersTarget(sub, args[1:], stderr)
	if code >= 0 {
		return code
	}
	if sub == "revoke" {
		return usersRevoke(store, id, stderr)
	}
	return usersAdd(store, id, stdout, stderr)
}

const usersUsage = "usage: kbengine users add|revoke --tenants <s3://bucket | dir> --id <name>"

// usersTarget parses the flags into the store and the person. code ≥ 0 — выйти
// с этим кодом.
func usersTarget(sub string, args []string, stderr io.Writer) (blob.Store, domain.UserID, int) {
	fs := flag.NewFlagSet("users "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	loc := fs.String("tenants", "", "s3://bucket or directory, the same as serve --tenants")
	s3TokenFile := fs.String("s3-token-file", "", "optional file with an IAM token for s3:// (mode 0600)")
	name := fs.String("id", "", "person's name: a-z, 0-9 and inner hyphens, up to 32")
	if code, stop := parseFlags(fs, args); stop {
		return nil, domain.UserID{}, code
	}
	if *loc == "" {
		fmt.Fprintf(stderr, "users %s: --tenants is required\n%s\n", sub, usersUsage)
		return nil, domain.UserID{}, 2
	}
	id, err := domain.NewUserID(*name)
	if err != nil {
		fmt.Fprintf(stderr, "users %s: %v\n", sub, err)
		return nil, domain.UserID{}, 2
	}
	store, err := openTenantStore(*loc, *s3TokenFile)
	if err != nil {
		fmt.Fprintf(stderr, "users %s: %v\n", sub, err)
		return nil, domain.UserID{}, 2
	}
	return store, id, -1
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
	if errors.Is(err, userauth.ErrUserExists) {
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
