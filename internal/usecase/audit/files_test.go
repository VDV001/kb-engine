package audit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/audit"
)

// fakeFiles answers for a fixed set of paths. Anything not listed is missing —
// which is the case this audit exists to name.
type fakeFiles struct {
	present map[string]bool
	err     error
}

func (f fakeFiles) Exists(file string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.present[file], nil
}

func filesCatalog(t *testing.T) *domain.Catalog {
	t.Helper()
	cat, err := domain.NewCatalog([]domain.Entry{
		article(t, 1, articleParams{title: "Есть на диске", lifecycle: "active", verdict: "keep"}),
		article(t, 2, articleParams{title: "Файла нет", lifecycle: "active", verdict: "keep"}),
		article(t, 3, articleParams{title: "Без файла вообще", lifecycle: "active", verdict: "keep"}),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return cat
}

// An audit that never looked must not read as a clean one.
func TestMissingFileIssues_refusesWithoutAProvider(t *testing.T) {
	_, err := audit.NewService(fakeLoader{catalog: filesCatalog(t)}).MissingFileIssues()
	if !errors.Is(err, audit.ErrNoArtefactFiles) {
		t.Fatalf("err = %v, want ErrNoArtefactFiles", err)
	}
}

func TestMissingFileIssues_reportsFailureFromTheProvider(t *testing.T) {
	svc := audit.NewService(fakeLoader{catalog: filesCatalogWithFiles(t)})
	svc.WithArtefactFiles(fakeFiles{err: errors.New("disk on fire")})

	_, err := svc.MissingFileIssues()
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("err = %v, want the provider's failure named", err)
	}
}

func TestMissingFileIssues_namesOnlyTheMissingOnes(t *testing.T) {
	svc := audit.NewService(fakeLoader{catalog: filesCatalogWithFiles(t)})
	svc.WithArtefactFiles(fakeFiles{present: map[string]bool{"notes/here.md": true}})

	findings, err := svc.MissingFileIssues()
	if err != nil {
		t.Fatalf("MissingFileIssues: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: %v", len(findings), findings)
	}
	if findings[0].EntryID != 2 {
		t.Errorf("finding is about entry %d, want 2", findings[0].EntryID)
	}
	if !strings.Contains(strings.Join(findings[0].Reasons, " "), "notes/gone.md") {
		t.Errorf("the reason does not name the path: %v", findings[0].Reasons)
	}
}

// The entry carrying no file at all is a different state from one pointing at a
// file that is gone, and must not be reported.
func filesCatalogWithFiles(t *testing.T) *domain.Catalog {
	t.Helper()
	cat, err := domain.NewCatalog([]domain.Entry{
		article(t, 1, articleParams{title: "Есть на диске", lifecycle: "active", verdict: "keep", file: "notes/here.md"}),
		article(t, 2, articleParams{title: "Файла нет", lifecycle: "active", verdict: "keep", file: "notes/gone.md"}),
		article(t, 3, articleParams{title: "Без файла вообще", lifecycle: "active", verdict: "keep"}),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return cat
}
