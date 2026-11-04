package search_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// full builds an entry with every searched field set.
func full(t *testing.T, id int, title, desc, author string, tags ...string) domain.Entry {
	t.Helper()
	cat, _ := domain.NewCategory("articles")
	life, _ := domain.NewLifecycle("active")
	e, err := domain.NewEntry(domain.EntryParams{
		ID: id, Kind: domain.KindArticle, Title: title, Description: desc, Author: author,
		Category: cat, Lifecycle: life, Tags: tags,
	})
	if err != nil {
		t.Fatalf("entry %d: %v", id, err)
	}
	return e
}

// Explain отвечает, ГДЕ и КАК совпало каждое слово запроса, — тем же кодом,
// что ищет: человек видел запись по «жо» без единого «жо» на экране, потому
// что совпал автор, которого в строке выдачи нет, и совпал транслитом.
func TestExplain(t *testing.T) {
	dict := search.New(search.Dictionary{
		"конкурентность": {Same: []string{"concurrency"}},
		"сборка мусора":  {Same: []string{"garbage collection"}},
	})
	tests := []struct {
		name  string
		e     domain.Entry
		query string
		want  []search.Why
	}{
		{"заголовок буквально", full(t, 1, "Kubernetes в проде", "", ""), "kubernetes",
			[]search.Why{{Word: "kubernetes", Field: search.FieldTitle, Text: "Kubernetes в проде", Hit: "Kubernetes", How: search.HowExact}}},
		{"автор транслитом", full(t, 2, "У облака есть корзина", "", "zhogov1985"), "жо",
			[]search.Why{{Word: "жо", Field: search.FieldAuthor, Text: "zhogov1985", Hit: "zhogov1985", How: search.HowTranslit}}},
		{"автор с начала слова", full(t, 3, "Copilot видит", "", "Александр Жогов"), "жо",
			[]search.Why{{Word: "жо", Field: search.FieldAuthor, Text: "Александр Жогов", Hit: "Жогов", How: search.HowExact}}},
		{"опечатка", full(t, 4, "Kubernetes в проде", "", ""), "kuberntes",
			[]search.Why{{Word: "kuberntes", Field: search.FieldTitle, Text: "Kubernetes в проде", Hit: "Kubernetes", How: search.HowTypo}}},
		{"тег", full(t, 5, "Сборка образов", "", "", "docker"), "docker",
			[]search.Why{{Word: "docker", Field: search.FieldTag, Text: "docker", Hit: "docker", How: search.HowExact}}},
		{"синоним", full(t, 6, "Concurrency in Go", "", ""), "конкурентность",
			[]search.Why{{Word: "конкурентность", Field: search.FieldTitle, Text: "Concurrency in Go", Hit: "Concurrency", How: search.HowSynonym}}},
		// Фраза переводится целиком: по словам «сборка» и «мусора» не находятся.
		{"фраза синонимом", full(t, 7, "Паузы", "garbage collection в Go", ""), "сборка мусора",
			[]search.Why{
				{Word: "сборка мусора", Field: search.FieldDescription, Text: "garbage collection в Go", Hit: "garbage", How: search.HowSynonym},
				{Word: "сборка мусора", Field: search.FieldDescription, Text: "garbage collection в Go", Hit: "collection", How: search.HowSynonym},
			}},
		{"номер", full(t, 8, "Что угодно", "", ""), "#8",
			[]search.Why{{Word: "#8", Field: search.FieldID, Text: "8", Hit: "8", How: search.HowExact}}},
		// Буквальное совпадение в любом поле важнее нечеткого в заголовке: так
		// же решает поиск — слой за слоем по всему тексту записи.
		{"буквально в описании раньше опечатки в заголовке", full(t, 9, "Kubernetes", "про kuberntes", ""), "kuberntes",
			[]search.Why{{Word: "kuberntes", Field: search.FieldDescription, Text: "про kuberntes", Hit: "kuberntes", How: search.HowExact}}},
		{"два слова — два ответа", full(t, 10, "Агенты и память", "", "zhogov1985"), "агент жо",
			[]search.Why{
				{Word: "агент", Field: search.FieldTitle, Text: "Агенты и память", Hit: "Агенты", How: search.HowExact},
				{Word: "жо", Field: search.FieldAuthor, Text: "zhogov1985", Hit: "zhogov1985", How: search.HowTranslit},
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := search.Explain(tt.e, tt.query, dict)
			if len(got) != len(tt.want) {
				t.Fatalf("Explain = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// Обязательство: все, что нашел FilterWith, Explain объясняет — по слову на
// каждое слово запроса (фраза синонимом — хотя бы одно). Объяснение, которое
// молчит о найденном, возвращает человека к «нашлось непонятно что».
func TestExplain_CoversEverythingFilterFinds(t *testing.T) {
	dict := search.New(search.Dictionary{"сборка мусора": {Same: []string{"garbage collection"}}})
	entries := []domain.Entry{
		full(t, 1, "Kubernetes в проде", "оркестрация контейнеров", "zhogov1985", "k8s", "devops"),
		full(t, 2, "Агенты и память", "garbage collection в Go", "Александр Жогов", "ai"),
		full(t, 3, "Промпт-инженерия", "как писать промты", "Иван", "llm"),
		full(t, 4, "Docker и CI/CD", "сборка образов", "", "докер"),
	}
	for _, q := range []string{"жо", "kuberntes", "агент жо", "сборка мусора", "промт", "ci/cd", "докер", "docker", "k8s", "#3", "оркестрац", "devops иван", "zhogov", "go"} {
		for _, e := range search.FilterWith(entries, q, dict) {
			why := search.Explain(e, q, dict)
			words := strings.Fields(q)
			if len(why) == 0 || (len(why) < len(words) && why[0].How != search.HowSynonym) {
				t.Errorf("%q нашел #%d, Explain = %+v", q, e.ID(), why)
			}
		}
	}
}
