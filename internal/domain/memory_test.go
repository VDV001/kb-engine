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
	topicList := "# Горячая\n\n" + domain.TopicsStart + "\n- финансы\n" + domain.TopicsEnd + "\n"
	good := domain.ColdNote{File: "a.md", Name: "a", Topic: "финансы", Description: "про a"}
	idxA := []domain.IndexEntry{{File: "a.md", Hook: "про a"}}
	tests := []struct {
		name string
		in   domain.MemoryState
		want []domain.ProblemKind
	}{
		{"чисто", domain.MemoryState{Hot: topicList, HotLimit: 1000, Notes: []domain.ColdNote{good}, Index: idxA}, nil},
		// 8 кириллических букв — 16 байт: считать надо знаки, а не байты.
		{"кириллица считается знаками", domain.MemoryState{Hot: "яяяяяяяя", HotLimit: 10}, nil},
		{"горячий больше лимита", domain.MemoryState{Hot: strings.Repeat("я", 11), HotLimit: 10}, []domain.ProblemKind{domain.ProblemHotTooBig}},
		{"запись без описания", domain.MemoryState{Hot: topicList, HotLimit: 1000, Notes: []domain.ColdNote{{File: "b.md", Name: "b", Topic: "финансы"}}, Index: []domain.IndexEntry{{File: "b.md", Hook: "x"}}}, []domain.ProblemKind{domain.ProblemNoDescription}},
		{"записи нет в индексе", domain.MemoryState{Hot: topicList, HotLimit: 1000, Notes: []domain.ColdNote{good}}, []domain.ProblemKind{domain.ProblemNotIndexed}},
		{"индекс ведет в никуда", domain.MemoryState{HotLimit: 10, Index: []domain.IndexEntry{{File: "gone.md", Hook: "x"}}}, []domain.ProblemKind{domain.ProblemDangling}},
		{"длинный крючок", domain.MemoryState{Hot: topicList, HotLimit: 1000, Notes: []domain.ColdNote{good}, Index: []domain.IndexEntry{{File: "a.md", Hook: strings.Repeat("я", 151)}}}, []domain.ProblemKind{domain.ProblemLongHook}},
		// ADR-0008: пустой указатель на холодный слой теряет правила (19/30 на haiku).
		{"записи есть, перечня тем в горячем нет", domain.MemoryState{Hot: "правила", HotLimit: 1000, Notes: []domain.ColdNote{good}, Index: idxA}, []domain.ProblemKind{domain.ProblemNoTopicList}},
		{"метки перечня есть, внутри пусто", domain.MemoryState{Hot: domain.TopicsStart + "\n  \n" + domain.TopicsEnd, HotLimit: 1000, Notes: []domain.ColdNote{good}, Index: idxA}, []domain.ProblemKind{domain.ProblemNoTopicList}},
		{"записей нет — перечень не нужен", domain.MemoryState{Hot: "правила", HotLimit: 1000}, nil},
		{"запись без темы", domain.MemoryState{Hot: topicList, HotLimit: 1000, Notes: []domain.ColdNote{{File: "a.md", Name: "a", Description: "про a"}}, Index: idxA}, []domain.ProblemKind{domain.ProblemNoTopic}},
		{"индекс выше потолка", domain.MemoryState{Hot: topicList, HotLimit: 1000, IndexRunes: 101, IndexLimit: 100, Notes: []domain.ColdNote{good}, Index: idxA}, []domain.ProblemKind{domain.ProblemIndexTooBig}},
		{"индекс ровно в потолок", domain.MemoryState{Hot: topicList, HotLimit: 1000, IndexRunes: 100, IndexLimit: 100, Notes: []domain.ColdNote{good}, Index: idxA}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := kinds(domain.CheckMemory(tt.in))
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
		name, slug, topic, desc string
		ok                      bool
	}{
		{"обычная", "owner-prefers-short", "финансы", "владелец любит коротко", true},
		{"пустое имя", "", "финансы", "x", false},
		{"имя с пробелом", "two words", "финансы", "x", false},
		{"имя с заглавной", "Owner", "финансы", "x", false},
		{"имя с точкой и слэшем", "../x", "финансы", "x", false},
		{"пустое описание", "ok", "финансы", "  ", false},
		{"описание с переводом строки", "ok", "финансы", "a\nb", false},
		{"пустая тема", "ok", "  ", "x", false},
		{"тема с переводом строки", "ok", "a\nb", "x", false},
		{"тема длиннее потолка", "ok", strings.Repeat("я", 41), "x", false},
		{"описание длиннее потолка", "ok", "финансы", strings.Repeat("я", 151), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := domain.NewColdNote(tt.slug, tt.topic, tt.desc)
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
