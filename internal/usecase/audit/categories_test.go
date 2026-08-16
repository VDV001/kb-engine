package audit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/audit"
)

// categoriesCatalog собирает каталог, объявивший две категории, и записи в
// категориях, которые в этом словаре есть и которых в нём нет.
func categoriesCatalog(t *testing.T, labels map[string]string, cats ...string) *domain.Catalog {
	t.Helper()
	entries := make([]domain.Entry, 0, len(cats))
	for i, c := range cats {
		entries = append(entries, article(t, i+1, articleParams{
			title: "Запись " + c, category: c, lifecycle: "active", verdict: "keep",
		}))
	}
	cat, err := domain.NewCatalog(entries, domain.WithCategoryLabels(labels))
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return cat
}

// Проверка, которой нечем ответить, не должна выглядеть чистой: каталог без
// словаря категорий не значит, что все категории выдуманы, — он значит, что
// сверять не с чем. То же правило держат MissingFileIssues и VersionDriftIssues.
func TestUndeclaredCategoryIssues_refusesWithoutDeclaredCategories(t *testing.T) {
	cat := categoriesCatalog(t, nil, "golang")
	_, err := audit.NewService(fakeLoader{catalog: cat}).UndeclaredCategoryIssues()
	if !errors.Is(err, audit.ErrNoDeclaredCategories) {
		t.Fatalf("err = %v, want ErrNoDeclaredCategories", err)
	}
}

func TestUndeclaredCategoryIssues(t *testing.T) {
	labels := map[string]string{"golang": "Go", "management": "Менеджмент"}

	tests := []struct {
		name       string
		categories []string
		wantIDs    []int
		wantNamed  string
	}{
		{
			name:       "все категории объявлены",
			categories: []string{"golang", "management"},
		},
		{
			name:       "одна запись в необъявленной категории",
			categories: []string{"golang", "testing"},
			wantIDs:    []int{2},
			wantNamed:  "testing",
		},
		{
			name:       "несколько записей в одной необъявленной",
			categories: []string{"testing", "golang", "testing"},
			wantIDs:    []int{1, 3},
			wantNamed:  "testing",
		},
		{
			name:       "две разные необъявленные",
			categories: []string{"testing", "devops"},
			wantIDs:    []int{1, 2},
			wantNamed:  "devops",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cat := categoriesCatalog(t, labels, tc.categories...)
			findings, err := audit.NewService(fakeLoader{catalog: cat}).UndeclaredCategoryIssues()
			if err != nil {
				t.Fatalf("UndeclaredCategoryIssues: %v", err)
			}
			var gotIDs []int
			for _, f := range findings {
				gotIDs = append(gotIDs, f.EntryID)
			}
			if len(gotIDs) != len(tc.wantIDs) {
				t.Fatalf("находок %v, ждали %v", gotIDs, tc.wantIDs)
			}
			for i, want := range tc.wantIDs {
				if gotIDs[i] != want {
					t.Fatalf("находки %v, ждали %v", gotIDs, tc.wantIDs)
				}
			}
			if tc.wantNamed == "" {
				return
			}
			// Находка обязана называть саму категорию: без неё непонятно, что
			// чинить — переименовать запись или дописать словарь.
			joined := strings.Join(findings[len(findings)-1].Reasons, " ")
			if !strings.Contains(joined, tc.wantNamed) {
				t.Fatalf("причина %q не называет категорию %q", joined, tc.wantNamed)
			}
		})
	}
}

// Обратная сторона — не находка: объявленная категория без записей законна,
// пустой раздел это состояние базы, а не дефект.
func TestUnusedCategories(t *testing.T) {
	labels := map[string]string{"golang": "Go", "management": "Менеджмент", "devops": "DevOps"}
	cat := categoriesCatalog(t, labels, "golang")

	svc := audit.NewService(fakeLoader{catalog: cat})

	findings, err := svc.UndeclaredCategoryIssues()
	if err != nil {
		t.Fatalf("UndeclaredCategoryIssues: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("пустая категория дала находки: %v", findings)
	}

	unused, err := svc.UnusedCategories()
	if err != nil {
		t.Fatalf("UnusedCategories: %v", err)
	}
	want := []string{"devops", "management"}
	if len(unused) != len(want) {
		t.Fatalf("unused = %v, want %v", unused, want)
	}
	for i := range want {
		if unused[i] != want[i] {
			t.Fatalf("unused = %v, want %v (порядок устойчивый)", unused, want)
		}
	}
}
