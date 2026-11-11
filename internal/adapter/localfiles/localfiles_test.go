package localfiles_test

import (
	"context"
	"errors"
	"fmt"
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
	err := s.Walk(context.Background(), func(d domain.FileDoc) error {
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
		// Слово в имени файла не отсекается: заметка о бэкапах — не бэкап.
		write(t, root, "notes/backup-strategy.md", "стратегия"),
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
		// Копии, а не оригиналы: десять бэкапов каталога по 2 МБ забивали
		// выдачу search_files раньше живых файлов (живой прогон #375).
		"knowledge-base/_data/.backup/catalog.2026-10-17.json",
		"backups/2026-08-21_cleanup/settings.json.stale-backup-2026-06-01",
		"Backup/old.md",
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

func TestExclusions_IgnoreCase(t *testing.T) {
	// APFS регистр не различает: FINANCES/ledger.md открывает finances/ledger.md.
	// Сравнение с учетом регистра пропускало исключенную папку (ревью #371).
	root := t.TempDir()
	write(t, root, "finances/ledger.md", "деньги")
	write(t, root, "notes.BAK.md", "старое")
	write(t, root, ".ENV", "TOKEN=1")
	keep := write(t, root, "plan.md", "план")
	s, err := localfiles.New([]string{root}, []string{"finances"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(root, "FINANCES", "ledger.md"),
		filepath.Join(root, "notes.BAK.md"),
		filepath.Join(root, ".ENV"),
	} {
		if _, err := s.ReadText(p); !errors.Is(err, localfiles.ErrNotAllowed) {
			t.Errorf("ReadText(%q) err = %v, want ErrNotAllowed", p, err)
		}
		if err := s.Check(p); !errors.Is(err, localfiles.ErrNotAllowed) {
			t.Errorf("Check(%q) err = %v, want ErrNotAllowed", p, err)
		}
	}
	if got := walked(t, s); !slices.Equal(got, []string{keep}) {
		t.Fatalf("walked = %q", got)
	}
}

func TestWalk_SymlinkedRoot(t *testing.T) {
	// Корень-симлинк (iCloud, /tmp на macOS) давал ноль файлов без ошибки.
	real := t.TempDir()
	write(t, real, "plan.md", "план")
	link := filepath.Join(t.TempDir(), "notes")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	s, err := localfiles.New([]string{link}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := walked(t, s); len(got) != 1 {
		t.Fatalf("walked = %q, want 1 file", got)
	}
}

func TestWalk_SkipsSecretFormats(t *testing.T) {
	root := t.TempDir()
	keep := []string{
		write(t, root, "tokenizer.go", "package x"), // слово token в имени кода — не секрет
		write(t, root, "notes/credits.md", "титры"),
		// заметка О секретах — не секрет: слова в имени проверяются только у конфигов
		write(t, root, "handoffs/2026-10-17_close-dotfiles-secrets-cleanup.md", "чистка"),
	}
	for _, rel := range []string{
		"credentials.json", "gcp/service-account.json", "service_account.json", "secrets.yaml",
		"app/secret.toml", ".npmrc", ".netrc", ".pgpass", ".envrc", ".pypirc", "kubeconfig",
		"infra/terraform.tfstate", "infra/terraform.tfstate.backup",
	} {
		write(t, root, rel, "secret")
	}
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(keep)
	if got := walked(t, s); !slices.Equal(got, keep) {
		t.Fatalf("walked:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(keep, "\n"))
	}
}

func TestCheck_AllowsWhatTheWalkWouldIndex(t *testing.T) {
	root := t.TempDir()
	ok := write(t, root, "plan.md", "план")
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Check(ok); err != nil {
		t.Fatalf("Check(ok) = %v", err)
	}
}

func TestWalk_StopsWhenContextIsCancelled(t *testing.T) {
	root := t.TempDir()
	for i := range 5 {
		write(t, root, fmt.Sprintf("f%d.md", i), "x")
	}
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = s.Walk(ctx, func(domain.FileDoc) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// Граница корня (#542): ReadText и Check получают путь снаружи (MCP, окно),
// и ни одна форма пути не должна выводить за папки окна.
func TestReadText_StaysInsideRoots(t *testing.T) {
	root := t.TempDir()
	write(t, root, "inside.md", "свое")
	outsideDir := t.TempDir()
	secret := write(t, outsideDir, "secret.md", "чужое")
	// Папка-ссылка наружу: сам файл за ней обычный, ссылка стоит в середине пути.
	if err := os.Symlink(outsideDir, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	// Ссылка на файл снаружи с безобидным именем.
	if err := os.Symlink(secret, filepath.Join(root, "alias.md")); err != nil {
		t.Fatal(err)
	}
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	tests := []struct{ name, path string }{
		{"абсолютный вне папок", secret},
		{"относительный", "inside.md"},
		{"относительный с ..", filepath.Join("..", filepath.Base(outsideDir), "secret.md")},
		{"абсолютный с .. наружу", filepath.Join(root, "..", filepath.Base(outsideDir), "secret.md")},
		{"через папку-ссылку", filepath.Join(root, "linked", "secret.md")},
		{"ссылка на файл снаружи", filepath.Join(root, "alias.md")},
		{"сам корень", root},
		{"корень с .. и обратно", filepath.Join(root, "..", filepath.Base(root))},
		{"префикс-двойник корня", root + "-twin/inside.md"},
		{"пустой путь", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.ReadText(tt.path); !errors.Is(err, localfiles.ErrNotAllowed) {
				t.Errorf("ReadText(%q) err = %v, want ErrNotAllowed", tt.path, err)
			}
			if err := s.Check(tt.path); !errors.Is(err, localfiles.ErrNotAllowed) {
				t.Errorf("Check(%q) err = %v, want ErrNotAllowed", tt.path, err)
			}
		})
	}
	if got, err := s.ReadText(filepath.Join(root, "inside.md")); err != nil || got != "свое" {
		t.Fatalf("свой файл должен читаться: %q, %v", got, err)
	}
}

// Обход тоже не выходит за корень по ссылкам: чужой файл не попадает в выдачу
// search_files, значит read_file за ним не придет.
func TestWalk_DoesNotFollowSymlinksOutside(t *testing.T) {
	root := t.TempDir()
	write(t, root, "inside.md", "свое")
	outsideDir := t.TempDir()
	write(t, outsideDir, "secret.md", "чужое")
	if err := os.Symlink(outsideDir, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outsideDir, "secret.md"), filepath.Join(root, "alias.md")); err != nil {
		t.Fatal(err)
	}
	s, err := localfiles.New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, p := range walked(t, s) {
		if strings.Contains(p, "secret") || strings.Contains(p, "alias") || strings.Contains(p, "linked") {
			t.Errorf("в обходе путь за границей: %q", p)
		}
	}
}
