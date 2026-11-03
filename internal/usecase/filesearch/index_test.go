package filesearch_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

func TestTokens(t *testing.T) {
	got := filesearch.Tokens("Ёлка, договоры и Kubernetes-кластер: v2 x")
	// Однобуквенное («и», «x») не индексируется: это шум, а не слово поиска.
	want := []string{"елка", "договоры", "kubernetes", "кластер", "v2"}
	if !slices.Equal(got, want) {
		t.Fatalf("Tokens = %q, want %q", got, want)
	}
}

func TestStem_RussianFormsMeet(t *testing.T) {
	// Формы одного слова обязаны сойтись в одну основу: без этого запрос
	// «договор» не находит файл, где написано «договора», а именно так
	// пишут по-русски.
	for _, group := range [][]string{
		{"договор", "договора", "договоры", "договоров", "договорами"},
		// «заявок» не входит: беглая гласная, потолок срезания окончаний.
		{"заявка", "заявки", "заявку", "заявкой"},
		{"оплата", "оплаты", "оплату", "оплатой"},
	} {
		base := filesearch.Stem(group[0])
		for _, w := range group[1:] {
			if got := filesearch.Stem(w); got != base {
				t.Errorf("Stem(%q) = %q, Stem(%q) = %q", w, got, group[0], base)
			}
		}
	}
}

func TestStem_KeepsShortAndLatin(t *testing.T) {
	for _, w := range []string{"кот", "api", "kubernetes", "v2"} {
		if got := filesearch.Stem(w); got != w {
			t.Errorf("Stem(%q) = %q, want unchanged", w, got)
		}
	}
}

func doc(t *testing.T, path string) domain.FileDoc {
	t.Helper()
	d, err := domain.NewFileDoc(path, 1, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func paths(hits []filesearch.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Doc.Path())
	}
	return out
}

func TestIndex_SearchFindsWordForms(t *testing.T) {
	ix := filesearch.NewIndex()
	ix.Add(doc(t, "/w/a.md"), "Подписали договора с поставщиком")
	ix.Add(doc(t, "/w/b.md"), "Заявка на оплату")
	ix.Add(doc(t, "/w/c.md"), "Ничего общего")

	if got := paths(ix.Search("договор", 10)); !slices.Equal(got, []string{"/w/a.md"}) {
		t.Fatalf("договор -> %q", got)
	}
}

func TestIndex_AllTermsRequired(t *testing.T) {
	ix := filesearch.NewIndex()
	ix.Add(doc(t, "/w/a.md"), "заявка поставщику")
	ix.Add(doc(t, "/w/b.md"), "заявка на оплату")

	if got := paths(ix.Search("заявки оплата", 10)); !slices.Equal(got, []string{"/w/b.md"}) {
		t.Fatalf("заявки оплата -> %q", got)
	}
}

func TestIndex_RanksMoreMentionsHigherAndLimits(t *testing.T) {
	ix := filesearch.NewIndex()
	ix.Add(doc(t, "/w/once.md"), "агент один раз и много прочих слов вокруг")
	ix.Add(doc(t, "/w/often.md"), "агент агенты агента")
	ix.Add(doc(t, "/w/twice.md"), "агент и агенты")

	got := paths(ix.Search("агент", 2))
	if !slices.Equal(got, []string{"/w/often.md", "/w/twice.md"}) {
		t.Fatalf("ранжирование/лимит: %q", got)
	}
}

func TestIndex_FileNameCounts(t *testing.T) {
	// Имя файла тоже текст: «бюджет ташкента» ищут по имени чаще, чем по
	// содержимому.
	ix := filesearch.NewIndex()
	ix.Add(doc(t, "/w/tashkent_budget.py"), "print(1)")
	if got := paths(ix.Search("budget", 10)); !slices.Equal(got, []string{"/w/tashkent_budget.py"}) {
		t.Fatalf("budget -> %q", got)
	}
}

func TestIndex_EmptyQuery(t *testing.T) {
	ix := filesearch.NewIndex()
	ix.Add(doc(t, "/w/a.md"), "текст")
	if hits := ix.Search("  , ", 10); len(hits) != 0 {
		t.Fatalf("пустой запрос дал %d", len(hits))
	}
}

func TestSnippet(t *testing.T) {
	text := "первая строка\nвторая строка\nтут Договоры подписаны\nчетвертая"
	got := filesearch.Snippet(text, "договор", 80)
	if !strings.Contains(got, "Договоры подписаны") {
		t.Fatalf("Snippet = %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("сниппет должен быть одной строкой: %q", got)
	}
	if got := filesearch.Snippet(text, "отсутствует", 80); got != "первая строка" {
		t.Fatalf("без совпадения ждем первую строку, получили %q", got)
	}
}

type fakeSource struct {
	files map[string]string
	fail  string
}

func (f fakeSource) Walk(fn func(domain.FileDoc) error) error {
	keys := make([]string, 0, len(f.files))
	for k := range f.files {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		d, err := domain.NewFileDoc(k, int64(len(f.files[k])), time.Time{})
		if err != nil {
			return err
		}
		if err := fn(d); err != nil {
			return err
		}
	}
	return nil
}

func (f fakeSource) ReadText(path string) (string, error) {
	if path == f.fail {
		return "", errors.New("boom")
	}
	return f.files[path], nil
}

func TestService_BuildAndSearch(t *testing.T) {
	src := fakeSource{files: map[string]string{
		"/w/a.md": "строка\nПодписали договора",
		"/w/b.md": "другое",
		"/w/c.md": "битый",
	}, fail: "/w/c.md"}
	svc := filesearch.NewService(src)

	st, err := svc.Build()
	if err != nil {
		t.Fatal(err)
	}
	// Нечитаемый файл не роняет сборку, но и не теряется молча: он посчитан.
	if st.Indexed != 2 || st.Skipped != 1 {
		t.Fatalf("stats = %+v", st)
	}

	hits, err := svc.Search("договор", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Doc.Path() != "/w/a.md" || hits[0].Snippet != "Подписали договора" {
		t.Fatalf("hits = %+v", hits)
	}
}
