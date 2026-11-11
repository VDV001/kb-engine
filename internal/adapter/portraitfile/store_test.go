package portraitfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/portraitfile"
	"github.com/daniil/kb-engine/internal/domain"
)

// Портрет — файл portrait.json в папке человека, только для его
// пользователя (0600); файла нет — портрет пуст, а не ошибка.
func TestStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := portraitfile.Store{Dir: dir}
	p, err := s.Load(t.Context())
	if err != nil || len(p.Facts()) != 0 {
		t.Fatalf("пустой: %+v %v", p, err)
	}
	at := time.Date(2026, 11, 6, 21, 0, 0, 0, time.UTC)
	f1, _ := domain.NewFact("f1", domain.SectionWork, "Пишет на Go", domain.OriginManual, domain.FactApproved, at)
	f2, _ := domain.NewFact("f2", domain.SectionPrefs, "Отвечать коротко", domain.OriginChat, domain.FactPending, at)
	p, _ = domain.NewPortrait([]domain.Fact{f1, f2})
	if err := s.Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, portraitfile.Name))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("права = %v, %v", st.Mode().Perm(), err)
	}
	got, err := s.Load(t.Context())
	if err != nil || len(got.Facts()) != 2 {
		t.Fatalf("прочитано: %+v %v", got, err)
	}
	g := got.Pending()[0]
	if g.ID() != "f2" || g.Section() != domain.SectionPrefs || g.Text() != "Отвечать коротко" || g.Origin() != domain.OriginChat || !g.At().Equal(at) {
		t.Fatalf("факт = %+v", g)
	}
}

// Испорченный файл не превращается в пустой портрет: сохранение поверх
// стерло бы все, что человек собрал.
func TestStore_Corrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, portraitfile.Name), []byte("{не json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (portraitfile.Store{Dir: dir}).Load(t.Context()); !errors.Is(err, portraitfile.ErrCorrupt) {
		t.Fatalf("испорченный = %v", err)
	}
	if _, err := (portraitfile.Store{}).Load(t.Context()); !errors.Is(err, portraitfile.ErrNoDir) {
		t.Fatalf("без папки = %v", err)
	}
}

// Запись портрета (#542) идет только в Dir/portrait.json. Если на месте файла
// лежит ссылка наружу, Save заменяет ссылку, а не пишет сквозь нее: чужой файл
// остается нетронутым.
func TestStore_SaveDoesNotWriteThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("не трогать"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, portraitfile.Name)); err != nil {
		t.Fatal(err)
	}
	s := portraitfile.Store{Dir: dir}
	f, _ := domain.NewFact("f1", domain.SectionWork, "Пишет на Go", domain.OriginManual, domain.FactApproved, time.Date(2026, 11, 6, 21, 0, 0, 0, time.UTC))
	p, _ := domain.NewPortrait([]domain.Fact{f})
	if err := s.Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "не трогать" {
		t.Fatalf("Save записал сквозь ссылку: %q", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != portraitfile.Name {
			t.Errorf("в папке остался лишний файл %q", e.Name())
		}
	}
}

// Без папки запись отказывает, а не пишет в текущий каталог процесса.
func TestStore_SaveWithoutDirRefuses(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	p, _ := domain.NewPortrait(nil)
	if err := (portraitfile.Store{}).Save(t.Context(), p); !errors.Is(err, portraitfile.ErrNoDir) {
		t.Fatalf("err = %v, want ErrNoDir", err)
	}
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Fatalf("в текущем каталоге появилось: %v", entries)
	}
}
