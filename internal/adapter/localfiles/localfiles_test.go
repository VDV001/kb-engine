package localfiles_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/domain"
)

func write(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func walked(t *testing.T, s *localfiles.Source) []string {
	t.Helper()
	var out []string
	err := s.Walk(func(d domain.FileDoc) error {
		out = append(out, d.Path())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func TestWalk_SkipsSecretsJunkAndBinaries(t *testing.T) {
	root := t.TempDir()
	keep := []string{
		write(t, root, "notes/plan.md", "план"),
		write(t, root, ".claude/handoffs/h.md", "handoff"),
		write(t, root, "src/main.go", "package main"),
	}
	for _, rel := range []string{
		".git/config",
		"web/node_modules/x/index.js",
		".venv/lib/a.py",
		"finances/ledger.md",
		".env",
		".env.local",
		"keys/server.pem",
		"keys/id_ed25519",
		"memory/x.md.bak-2026-10-13",
		"img/shot.png",
	} {
		write(t, root, rel, "secret")
	}
	s, err := localfiles.New([]string{root}, []string{"finances"})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(keep)
	if got := walked(t, s); !slices.Equal(got, keep) {
		t.Fatalf("walked:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(keep, "\n"))
	}
}

func TestWalk_SkipsLargeFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "big.md", strings.Repeat("a", localfiles.MaxFileBytes+1))
	small := write(t, root, "small.md", "a")
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := walked(t, s); !slices.Equal(got, []string{small}) {
		t.Fatalf("walked = %q", got)
	}
}

func TestReadText(t *testing.T) {
	root := t.TempDir()
	ok := write(t, root, "a.md", "привет")
	bad := write(t, root, "b.txt", "\xff\xfe\x00broken")
	secret := write(t, root, ".env", "TOKEN=1")
	outside := write(t, t.TempDir(), "c.md", "чужое")
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := s.ReadText(ok); err != nil || got != "привет" {
		t.Fatalf("ReadText(ok) = %q, %v", got, err)
	}
	if _, err := s.ReadText(bad); err == nil {
		t.Fatal("не UTF-8 обязан отказать")
	}
	// ReadText — граница доверия: через MCP путь приходит снаружи, и читать
	// можно только то, что обход сам бы проиндексировал.
	for _, p := range []string{secret, outside, filepath.Join(root, "..", filepath.Base(root), ".env")} {
		if _, err := s.ReadText(p); !errors.Is(err, localfiles.ErrNotAllowed) {
			t.Errorf("ReadText(%q) err = %v, want ErrNotAllowed", p, err)
		}
	}
}

func TestNew_RejectsRelativeRoot(t *testing.T) {
	if _, err := localfiles.New([]string{"relative/dir"}, nil); err == nil {
		t.Fatal("относительный корень обязан отказать")
	}
}

func TestReadText_RefusesSymlinks(t *testing.T) {
	// Обход симлинки не индексирует, значит и читать по ним нечего. Ссылка с
	// безобидным именем на .env или на исключенную папку — обход проверки
	// имени, а не путь, который человек нашел в выдаче.
	root := t.TempDir()
	write(t, root, ".env", "TOKEN=1")
	write(t, root, "finances/ledger.md", "деньги")
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, "notes.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "finances"), filepath.Join(root, "money")); err != nil {
		t.Fatal(err)
	}
	s, err := localfiles.New([]string{root}, []string{"finances"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(root, "notes.md"), filepath.Join(root, "money", "ledger.md")} {
		if _, err := s.ReadText(p); !errors.Is(err, localfiles.ErrNotAllowed) {
			t.Errorf("ReadText(%q) err = %v, want ErrNotAllowed", p, err)
		}
	}
}
