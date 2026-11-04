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
	read, _ := domain.NewReadState("read")
	e, err := domain.NewEntry(domain.EntryParams{
		ID: id, Kind: domain.KindArticle, Title: title, Description: desc, Author: author,
		Category: cat, Lifecycle: life, ReadState: &read, Tags: tags,
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
			[]search.Why{{Word: "kubernetes", Field: search.FieldTitle, Hit: "Kubernetes", How: search.HowExact}}},
		{"автор транслитом", full(t, 2, "У облака есть корзина", "", "zhogov1985"), "жо",
			[]search.Why{{Word: "жо", Field: search.FieldAuthor, Hit: "zhogov1985", How: search.HowTranslit}}},
		{"автор с начала слова", full(t, 3, "Copilot видит", "", "Александр Жогов"), "жо",
			[]search.Why{{Word: "жо", Field: search.FieldAuthor, Hit: "Жогов", How: search.HowExact}}},
		// Латинский запрос к кириллице — транслит, а не опечатка: правок ноль.
		{"латиница против кириллицы", full(t, 11, "Copilot видит", "", "Александр Жогов"), "zhogov",
			[]search.Why{{Word: "zhogov", Field: search.FieldAuthor, Hit: "Жогов", How: search.HowTranslit}}},
		// Одинаковый слой в двух полях — побеждает поле раньше (заголовок).
		{"ничья — поле раньше", full(t, 12, "Жогов о бэкапах", "", "Жогов"), "жогов",
			[]search.Why{{Word: "жогов", Field: search.FieldTitle, Hit: "Жогов", How: search.HowExact}}},
		{"опечатка", full(t, 4, "Kubernetes в проде", "", ""), "kuberntes",
			[]search.Why{{Word: "kuberntes", Field: search.FieldTitle, Hit: "Kubernetes", How: search.HowTypo}}},
		{"тег", full(t, 5, "Сборка образов", "", "", "docker"), "docker",
			[]search.Why{{Word: "docker", Field: search.FieldTag, Hit: "docker", How: search.HowExact}}},
		{"синоним", full(t, 6, "Concurrency in Go", "", ""), "конкурентность",
			[]search.Why{{Word: "конкурентность", Field: search.FieldTitle, Hit: "Concurrency", How: search.HowSynonym}}},
		// Фраза переводится целиком: по словам «сборка» и «мусора» не находятся.
		{"фраза синонимом", full(t, 7, "Паузы", "garbage collection в Go", ""), "сборка мусора",
			[]search.Why{
				{Word: "сборка мусора", Field: search.FieldDescription, Hit: "garbage", How: search.HowSynonym},
				{Word: "сборка мусора", Field: search.FieldDescription, Hit: "collection", How: search.HowSynonym},
			}},
		{"номер", full(t, 8, "Что угодно", "", ""), "#8",
			[]search.Why{{Word: "#8", Field: search.FieldID, Hit: "8", How: search.HowExact}}},
		// Буквальное совпадение в любом поле важнее нечеткого в заголовке: так
		// же решает поиск — слой за слоем по всему тексту записи.
		{"буквально в описании раньше опечатки в заголовке", full(t, 9, "Kubernetes", "про kuberntes", ""), "kuberntes",
			[]search.Why{{Word: "kuberntes", Field: search.FieldDescription, Hit: "kuberntes", How: search.HowExact}}},
		{"два слова — два ответа", full(t, 10, "Агенты и память", "", "zhogov1985"), "агент жо",
			[]search.Why{
				{Word: "агент", Field: search.FieldTitle, Hit: "Агенты", How: search.HowExact},
				{Word: "жо", Field: search.FieldAuthor, Hit: "zhogov1985", How: search.HowTranslit},
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

// Обратное обязательство: Explain не выдумывает совпадений, которых поиск не
// признает. Запись, что FilterWith не нашел, не получает объяснения на все
// слова: иначе на экране было бы «совпало» там, где совпадения нет, — короткое
// слово внутри чужого слова («go» в «ego»), слово дальше лимита опечаток.
func TestExplain_InventsNothing(t *testing.T) {
	dict := search.New(nil)
	entries := []domain.Entry{
		full(t, 1, "Алгоритмы и ego", "", ""),
		full(t, 2, "Kubxrnxtxs", "", ""),
		full(t, 3, "Пишем на Go", "", "zhogov1985"),
	}
	for _, q := range []string{"go", "kubernetes", "жо", "ego", "kubxrnxtxs"} {
		found := map[int]bool{}
		for _, e := range search.FilterWith(entries, q, dict) {
			found[e.ID()] = true
		}
		for _, e := range entries {
			if why := search.Explain(e, q, dict); !found[e.ID()] && len(why) == len(strings.Fields(q)) {
				t.Errorf("%q: #%d не найден, а Explain = %+v", q, e.ID(), why)
			}
		}
	}
}

// Случаи, на которых отдельная копия слоев в Explain разошлась с Matches
// (ревью #392): синоним однословного запроса, найденный транслитом; синоним
// из двух слов в разных полях; слово с дефисом, найденное транслитом во всем
// тексте поля. Поиск находит — объяснение обязано быть.
func TestExplain_CoversSynonymAndSeparatorPaths(t *testing.T) {
	dict := search.New(search.Dictionary{
		"k8s": {Same: []string{"kubernetes"}},
		"gc":  {Same: []string{"garbage collection"}},
	})
	entries := []domain.Entry{
		full(t, 1, "Кубернетес в проде", "", ""),
		full(t, 2, "garbage", "collection", ""),
		full(t, 3, "front-end заметки", "", ""),
	}
	for _, tt := range []struct {
		q  string
		id int
	}{{"k8s", 1}, {"gc", 2}, {"фронт-энд", 3}} {
		found := false
		for _, e := range search.FilterWith(entries, tt.q, dict) {
			if e.ID() != tt.id {
				continue
			}
			found = true
			if why := search.Explain(e, tt.q, dict); len(why) == 0 {
				t.Errorf("%q нашел #%d, а Explain пуст", tt.q, tt.id)
			}
		}
		if !found {
			t.Fatalf("%q не нашел #%d: фикстура не про то", tt.q, tt.id)
		}
	}
}

// Заглавная «Ё» — та же «е»: поиск понижает регистр до замены, объяснение
// обязано так же (ревью #392: «Ёжик» находился без объяснения, «Ёлкин»
// объяснялся опечаткой).
func TestExplain_CapitalYo(t *testing.T) {
	dict := search.New(nil)
	e1 := full(t, 1, "Ёжик в тумане", "", "")
	e2 := full(t, 2, "Заметка", "", "Пётр Ёлкин")
	if why := search.Explain(e1, "еж", dict); len(why) != 1 || why[0].Hit != "Ёжик" || why[0].How != search.HowExact {
		t.Fatalf("«еж» — Ёжик: %+v", why)
	}
	if why := search.Explain(e2, "елкин", dict); len(why) != 1 || why[0].How != search.HowExact || why[0].Field != search.FieldAuthor {
		t.Fatalf("«елкин» — Ёлкин: %+v", why)
	}
}

// Синоним из нескольких слов поиск находит и на стыке полей — он ищет по
// склеенному тексту записи. Объяснение идет по тому же тексту и называет поле,
// где совпадение начинается.
func TestExplain_SynonymAcrossFields(t *testing.T) {
	dict := search.New(search.Dictionary{"cicd": {Same: []string{"ci cd"}}})
	e := full(t, 1, "gucci", "cdn notes", "")
	if len(search.FilterWith([]domain.Entry{e}, "cicd", dict)) != 1 {
		t.Fatal("фикстура не про то: поиск не нашел")
	}
	why := search.Explain(e, "cicd", dict)
	if len(why) == 0 || why[0].How != search.HowSynonym || why[0].Field != search.FieldTitle {
		t.Fatalf("Explain = %+v", why)
	}
}

// Обратное обязательство и для фраз со словарем: запись, которую поиск не
// нашел, не получает полного объяснения ни словами, ни синонимом.
func TestExplain_InventsNothingWithDictionary(t *testing.T) {
	dict := search.New(search.Dictionary{
		"сборка мусора": {Same: []string{"garbage collection"}},
		"k8s":           {Same: []string{"kubernetes"}},
	})
	entries := []domain.Entry{
		full(t, 1, "garbage", "", ""),
		full(t, 2, "сборка проекта", "", ""),
		full(t, 3, "Kubernetic", "", ""),
	}
	for _, q := range []string{"сборка мусора", "k8s", "garbage collection", "сборка"} {
		found := map[int]bool{}
		for _, e := range search.FilterWith(entries, q, dict) {
			found[e.ID()] = true
		}
		for _, e := range entries {
			why := search.Explain(e, q, dict)
			if !found[e.ID()] && len(why) > 0 && (why[0].How == search.HowSynonym || len(why) == len(strings.Fields(q))) {
				t.Errorf("%q: #%d не найден, а Explain = %+v", q, e.ID(), why)
			}
		}
	}
}

// Склейка поиска и склейка объяснения — одна и та же строка: иначе поле,
// названное по позиции совпадения, было бы чужим. Пустое описание между
// заголовком и автором — место, где разделитель легко потерять.
func TestExplain_EmptyDescriptionKeepsFieldOffsets(t *testing.T) {
	dict := search.New(nil)
	e := full(t, 1, "Kubernetes в проде", "", "Иван")
	why := search.Explain(e, "kubernetes иван", dict)
	if len(why) != 2 || why[0].Field != search.FieldTitle || why[1].Field != search.FieldAuthor || why[1].Hit != "Иван" {
		t.Fatalf("Explain = %+v", why)
	}
}
