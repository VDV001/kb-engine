package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func kinds(ps []domain.MemoryProblem) []domain.ProblemKind {
	var out []domain.ProblemKind
	for _, p := range ps {
		out = append(out, p.Kind)
	}
	return out
}

func TestCheckMemory(t *testing.T) {
	good := domain.ColdNote{File: "a.md", Name: "a", Description: "про a"}
	tests := []struct {
		name  string
		hot   string
		limit int
		notes []domain.ColdNote
		index []domain.IndexEntry
		want  []domain.ProblemKind
	}{
		{"чисто", "коротко", 10, []domain.ColdNote{good}, []domain.IndexEntry{{File: "a.md", Hook: "про a"}}, nil},
		// 8 кириллических букв — 16 байт: считать надо знаки, а не байты.
		{"кириллица считается знаками", "яяяяяяяя", 10, nil, nil, nil},
		{"горячий больше лимита", strings.Repeat("я", 11), 10, nil, nil, []domain.ProblemKind{domain.ProblemHotTooBig}},
		{"запись без описания", "", 10, []domain.ColdNote{{File: "b.md", Name: "b"}}, []domain.IndexEntry{{File: "b.md", Hook: "x"}}, []domain.ProblemKind{domain.ProblemNoDescription}},
		{"записи нет в индексе", "", 10, []domain.ColdNote{good}, nil, []domain.ProblemKind{domain.ProblemNotIndexed}},
		{"индекс ведет в никуда", "", 10, nil, []domain.IndexEntry{{File: "gone.md", Hook: "x"}}, []domain.ProblemKind{domain.ProblemDangling}},
		{"длинный крючок", "", 10, []domain.ColdNote{good}, []domain.IndexEntry{{File: "a.md", Hook: strings.Repeat("я", 151)}}, []domain.ProblemKind{domain.ProblemLongHook}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := kinds(domain.CheckMemory(tt.hot, tt.limit, tt.notes, tt.index))
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestNewColdNote(t *testing.T) {
	tests := []struct {
		name, slug, desc string
		ok               bool
	}{
		{"обычная", "owner-prefers-short", "владелец любит коротко", true},
		{"пустое имя", "", "x", false},
		{"имя с пробелом", "two words", "x", false},
		{"имя с заглавной", "Owner", "x", false},
		{"имя с точкой и слэшем", "../x", "x", false},
		{"пустое описание", "ok", "  ", false},
		{"описание с переводом строки", "ok", "a\nb", false},
		{"описание длиннее потолка", "ok", strings.Repeat("я", 151), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := domain.NewColdNote(tt.slug, tt.desc)
			if tt.ok {
				if err != nil || n.File != tt.slug+".md" {
					t.Fatalf("got %+v, %v", n, err)
				}
				return
			}
			if !errors.Is(err, domain.ErrInvalidNote) {
				t.Errorf("err = %v, want ErrInvalidNote", err)
			}
		})
	}
}
