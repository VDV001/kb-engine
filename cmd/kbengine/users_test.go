package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
)

func TestUsersAddPrintsTheTokenOnce(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"users", "add", "--tenants", dir, "--id", "anna"}, &out, &errb); code != 0 {
		t.Fatalf("users add: exit %d, stderr %s", code, errb.String())
	}
	tok := strings.TrimSpace(out.String())
	if strings.ContainsAny(tok, " \n") || len(tok) < 32 {
		t.Fatalf("stdout = %q, want the token alone", out.String())
	}
	if !strings.Contains(errb.String(), "один раз") {
		t.Errorf("stderr %q does not say the token is shown once", errb.String())
	}
	s, err := blobfs.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := userauth.NewRegistry(context.Background(), s).Lookup(tok); !ok || got.String() != "anna" {
		t.Fatalf("Lookup(printed token) = %v, %v", got, ok)
	}
}

func TestUsersCommandRefusals(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"users", "add", "--tenants", dir, "--id", "anna"}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	cases := []struct {
		args []string
		code int
		says string
	}{
		{[]string{"users", "add", "--tenants", dir, "--id", "anna"}, 1, "already exists"},
		{[]string{"users", "add", "--tenants", dir, "--id", "Анна"}, 2, "invalid user id"},
		{[]string{"users", "add", "--id", "bob"}, 2, "--tenants"},
		{[]string{"users", "revoke", "--tenants", dir, "--id", "ghost"}, 1, "no such user"},
		{[]string{"users", "rename", "--tenants", dir, "--id", "anna"}, 2, "add"},
		{[]string{"users"}, 2, "add"},
	}
	for _, c := range cases {
		out.Reset()
		errb.Reset()
		code := run(c.args, &out, &errb)
		if code != c.code || !strings.Contains(errb.String(), c.says) {
			t.Errorf("%v: exit %d, stderr %q; want exit %d naming %q", c.args, code, errb.String(), c.code, c.says)
		}
		if out.Len() != 0 {
			t.Errorf("%v: stdout %q on a refusal", c.args, out.String())
		}
	}
}

func TestUsersRevokeClosesTheDoor(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{"users", "add", "--tenants", dir, "--id", "anna"}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	tok := strings.TrimSpace(out.String())
	out.Reset()
	if code := run([]string{"users", "revoke", "--tenants", dir, "--id", "anna"}, &out, &errb); code != 0 {
		t.Fatalf("revoke: exit %d, %s", code, errb.String())
	}
	s, err := blobfs.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := userauth.NewRegistry(context.Background(), s).Lookup(tok); ok {
		t.Fatal("revoked token still admitted")
	}
}
