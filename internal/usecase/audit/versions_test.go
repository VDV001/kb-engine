package audit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/audit"
)

// stubVersions answers with the version declared inside an artefact file.
type stubVersions struct {
	byFile map[string]string
	err    error
}

func (s stubVersions) VersionOf(file string) (domain.Version, bool, error) {
	if s.err != nil {
		return domain.Version{}, false, s.err
	}
	raw, ok := s.byFile[file]
	if !ok {
		return domain.Version{}, false, nil
	}
	v, err := domain.NewVersion(raw)
	if err != nil {
		return domain.Version{}, false, err
	}
	return v, true, nil
}

// ownArtefact builds a catalog entry for an owner artefact with the given
// catalog-side version.
func ownArtefact(t *testing.T, id int, file, version string) domain.Entry {
	t.Helper()
	cat, err := domain.NewCategory("standards")
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	rs, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	p := domain.EntryParams{
		ID: id, Kind: domain.KindArticle, Title: "Standard", Category: cat,
		Lifecycle: lc, ReadState: &rs, NotesFile: file,
	}
	if version != "" {
		v, err := domain.NewVersion(version)
		if err != nil {
			t.Fatalf("version: %v", err)
		}
		p.Version = &v
	}
	e, err := domain.NewEntry(p)
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	return e
}

func catalogOf(t *testing.T, entries ...domain.Entry) *domain.Catalog {
	t.Helper()
	c, err := domain.NewCatalog(entries)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	return c
}

type fixedLoader struct{ c *domain.Catalog }

func (l fixedLoader) Load() (*domain.Catalog, error) { return l.c, nil }

// The catalog holds a copy of a version that lives in the artefact file. The
// copy silently fell behind twice on live data: reflective-agent-defaults was
// 1.3.0 in the catalog against 1.5.1 in the file, team-handover 1.1.0 against
// 1.2.1. Nothing on any screen showed it.
func TestVersionDriftIssues(t *testing.T) {
	cases := []struct {
		name        string
		catalog     string
		file        string
		wantFinding bool
		wantReason  string
	}{
		{name: "in sync", catalog: "1.5.1", file: "1.5.1"},
		{name: "catalog behind", catalog: "1.3.0", file: "1.5.1", wantFinding: true, wantReason: "отстала"},
		{name: "catalog ahead", catalog: "2.0.0", file: "1.5.1", wantFinding: true, wantReason: "опережает"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := ownArtefact(t, 484, "standards/x/v1.md", c.catalog)
			svc := audit.NewService(fixedLoader{catalogOf(t, e)})
			svc.WithArtefactVersions(stubVersions{byFile: map[string]string{"standards/x/v1.md": c.file}})

			findings, err := svc.VersionDriftIssues()
			if err != nil {
				t.Fatalf("VersionDriftIssues: %v", err)
			}
			if !c.wantFinding {
				if len(findings) != 0 {
					t.Fatalf("got %d finding(s), want none: %+v", len(findings), findings)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("got %d finding(s), want 1", len(findings))
			}
			if findings[0].EntryID != 484 {
				t.Errorf("EntryID = %d, want 484", findings[0].EntryID)
			}
			joined := strings.Join(findings[0].Reasons, " ")
			if !strings.Contains(joined, c.wantReason) {
				t.Errorf("reasons %q do not say %q", joined, c.wantReason)
			}
			if !strings.Contains(joined, c.file) {
				t.Errorf("reasons %q do not name the file's version %q", joined, c.file)
			}
		})
	}
}

// A file that declares no version is the normal case for article drafts, whose
// version sits in the filename. Reporting those would bury the two real
// findings under noise.
func TestVersionDriftIssues_fileWithoutVersionIsSilent(t *testing.T) {
	e := ownArtefact(t, 790, "creations/habr/v5-final.md", "5.0.0")
	svc := audit.NewService(fixedLoader{catalogOf(t, e)})
	svc.WithArtefactVersions(stubVersions{byFile: map[string]string{}})

	findings, err := svc.VersionDriftIssues()
	if err != nil {
		t.Fatalf("VersionDriftIssues: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("got %d finding(s), want none: %+v", len(findings), findings)
	}
}

// Without a version provider the audit must say so rather than report a clean
// run: «no findings» from a check that never looked is the silence this engine
// keeps removing.
func TestVersionDriftIssues_withoutProviderIsAnError(t *testing.T) {
	e := ownArtefact(t, 484, "standards/x/v1.md", "1.3.0")
	svc := audit.NewService(fixedLoader{catalogOf(t, e)})

	if _, err := svc.VersionDriftIssues(); err == nil {
		t.Fatal("VersionDriftIssues without a provider returned no error")
	}
}

func TestVersionDriftIssues_readErrorIsReported(t *testing.T) {
	e := ownArtefact(t, 484, "standards/x/v1.md", "1.3.0")
	svc := audit.NewService(fixedLoader{catalogOf(t, e)})
	want := errors.New("disk on fire")
	svc.WithArtefactVersions(stubVersions{err: want})

	if _, err := svc.VersionDriftIssues(); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
