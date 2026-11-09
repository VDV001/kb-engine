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
		{"запись без темы", domain.MemoryState{Hot: topicList, HotLimit: 1000, Notes: []domain.ColdNote{good, {File: "b.md", Name: "b", Description: "про b"}}, Index: append([]domain.IndexEntry{{File: "b.md", Hook: "про b"}}, idxA...)}, []domain.ProblemKind{domain.ProblemNoTopic}},
		{"индекс выше потолка", domain.MemoryState{Hot: topicList, HotLimit: 1000, IndexRunes: 101, IndexLimit: 100, Notes: []domain.ColdNote{good}, Index: idxA}, []domain.ProblemKind{domain.ProblemIndexTooBig}},
		{"перечень отстал: в записях тема, которой в нем нет", domain.MemoryState{Hot: topicList, HotLimit: 1000, Notes: []domain.ColdNote{good, {File: "b.md", Name: "b", Topic: "здоровье", Description: "про b"}}, Index: append([]domain.IndexEntry{{File: "b.md", Hook: "про b"}}, idxA...)}, []domain.ProblemKind{domain.ProblemStaleTopics}},
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

func TestTopicList(t *testing.T) {
	notes := []domain.ColdNote{
		{Topic: "финансы"}, {Topic: "Здоровье"}, {Topic: "финансы"}, {Topic: "ФИНАНСЫ"}, {Topic: ""},
	}
	got := domain.TopicList(notes)
	want := []string{"Здоровье", "финансы"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("TopicList = %v, want %v (без повторов с разным регистром, без пустых, по алфавиту)", got, want)
	}
}

func TestReplaceTopics(t *testing.T) {
	notes := []domain.ColdNote{{Topic: "финансы"}, {Topic: "здоровье"}}
	hot := "до\n\n" + domain.TopicsStart + "\n- старое\n" + domain.TopicsEnd + "\n\nпосле\n"
	got, err := domain.ReplaceTopics(hot, notes)
	if err != nil {
		t.Fatal(err)
	}
	want := "до\n\n" + domain.TopicsStart + "\n- здоровье\n- финансы\n" + domain.TopicsEnd + "\n\nпосле\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	again, _ := domain.ReplaceTopics(got, notes)
	if again != got {
		t.Error("повторная замена меняет текст: перечень должен быть идемпотентным")
	}
	if _, err := domain.ReplaceTopics("без меток", notes); !errors.Is(err, domain.ErrNoTopicMarkers) {
		t.Errorf("без меток err = %v, want ErrNoTopicMarkers", err)
	}
	if _, err := domain.ReplaceTopics(domain.TopicsEnd+" "+domain.TopicsStart, notes); !errors.Is(err, domain.ErrNoTopicMarkers) {
		t.Errorf("метки в обратном порядке err = %v, want ErrNoTopicMarkers", err)
	}
}

func TestCheckMemory_staleVectors(t *testing.T) {
	topicList := domain.TopicsStart + "\n- финансы\n" + domain.TopicsEnd
	note := func(name, digest string) domain.ColdNote {
		return domain.ColdNote{File: name + ".md", Name: name, Topic: "финансы", Description: "d", Digest: digest}
	}
	idx := func(names ...string) []domain.IndexEntry {
		var out []domain.IndexEntry
		for _, n := range names {
			out = append(out, domain.IndexEntry{File: n + ".md", Hook: "d"})
		}
		return out
	}
	tests := []struct {
		name    string
		notes   []domain.ColdNote
		vectors map[string]string // nil — индекса векторов нет
		want    []domain.ProblemKind
	}{
		{"индекса векторов нет — слой необязателен", []domain.ColdNote{note("a", "1")}, nil, nil},
		{"векторы свежие", []domain.ColdNote{note("a", "1")}, map[string]string{"a": "1"}, nil},
		{"текст записи изменился", []domain.ColdNote{note("a", "2")}, map[string]string{"a": "1"}, []domain.ProblemKind{domain.ProblemStaleVectors}},
		{"у записи нет вектора", []domain.ColdNote{note("a", "1"), note("b", "1")}, map[string]string{"a": "1"}, []domain.ProblemKind{domain.ProblemStaleVectors}},
		{"вектор записи, которой нет", []domain.ColdNote{note("a", "1")}, map[string]string{"a": "1", "gone": "1"}, []domain.ProblemKind{domain.ProblemStaleVectors}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var names []string
			for _, n := range tt.notes {
				names = append(names, n.Name)
			}
			got := kinds(domain.CheckMemory(domain.MemoryState{
				Hot: topicList, HotLimit: 1000, Notes: tt.notes, Index: idx(names...),
				HasVectors: tt.vectors != nil, VectorDigests: tt.vectors,
			}))
			if len(got) != len(tt.want) || (len(got) == 1 && got[0] != tt.want[0]) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseOrigin(t *testing.T) {
	for in, want := range map[string]domain.Origin{"": domain.OriginHuman, "human": domain.OriginHuman, "agent": domain.OriginAgent} {
		got, err := domain.ParseOrigin(in)
		if err != nil || got != want {
			t.Errorf("ParseOrigin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := domain.ParseOrigin("robot"); !errors.Is(err, domain.ErrInvalidNote) {
		t.Errorf("robot: err = %v, want ErrInvalidNote", err)
	}
}

func TestAppendRule(t *testing.T) {
	const placeholder = "# Горячая\n\n## Правила работы\n\n-\n\n## Сейчас в работе\n\n-\n"
	tests := []struct {
		name, hot, rule, want string
		err                   error
	}{
		{"пустой маркер заменяется", placeholder, "не пушить в main",
			"# Горячая\n\n## Правила работы\n\n- не пушить в main\n\n## Сейчас в работе\n\n-\n", nil},
		{"дописывается после последнего правила, до следующего раздела",
			"## Правила работы\n- первое\n- второе\n\n## Дальше\n- x\n", "третье",
			"## Правила работы\n- первое\n- второе\n- третье\n\n## Дальше\n- x\n", nil},
		{"раздел последний в файле", "## Правила работы\n- первое", "второе", "## Правила работы\n- первое\n- второе\n", nil},
		{"нет раздела правил", "# Горячая\nтекст\n", "x", "", domain.ErrNoRulesSection},
		{"правило с переводом строки", placeholder, "a\nb", "", domain.ErrInvalidNote},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.AppendRule(tt.hot, tt.rule)
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if got != tt.want {
				t.Errorf("got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestReplacePortrait(t *testing.T) {
	hot := "# Горячая\n\n## Кто я\n\n" + domain.PortraitStart + "\nстарое\n" + domain.PortraitEnd + "\n\n## Правила работы\n- x\n"
	got, err := domain.ReplacePortrait(hot, "Обо мне: Разработчик.\nРабота: Тимлид.")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Горячая\n\n## Кто я\n\n" + domain.PortraitStart + "\nОбо мне: Разработчик.\nРабота: Тимлид.\n" + domain.PortraitEnd + "\n\n## Правила работы\n- x\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if again, _ := domain.ReplacePortrait(got, "Обо мне: Разработчик.\nРабота: Тимлид."); again != got {
		t.Error("повторная замена меняет текст")
	}
	empty, _ := domain.ReplacePortrait(got, "")
	if empty != "# Горячая\n\n## Кто я\n\n"+domain.PortraitStart+"\n"+domain.PortraitEnd+"\n\n## Правила работы\n- x\n" {
		t.Errorf("пустой портрет: %q", empty)
	}
	if _, err := domain.ReplacePortrait("без меток", "x"); !errors.Is(err, domain.ErrNoPortraitMarkers) {
		t.Errorf("без меток err = %v, want ErrNoPortraitMarkers", err)
	}
	// текст портрета не должен уметь закрыть блок раньше срока
	if _, err := domain.ReplacePortrait(hot, "хитрость "+domain.PortraitEnd+" дальше"); !errors.Is(err, domain.ErrInvalidNote) {
		t.Errorf("метка внутри текста err = %v, want ErrInvalidNote", err)
	}
}
