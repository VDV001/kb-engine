package search_test

import (
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// Фильтрация записей жила в adapter/tui, и веб позвать ее не мог — не потому,
// что «написана дважды», а потому что заперта в чужом адаптере. Отсюда замер
// issue #252: «кубернетес» давал 10 записей в терминале и ноль в браузере.
// Эти случаи закрепляют поведение НА УРОВНЕ USECASE, откуда его видят обе
// поверхности.
func entry(t *testing.T, id int, title string, tags ...string) domain.Entry {
	t.Helper()
	cat, err := domain.NewCategory("articles")
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	life, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	read, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("readstate: %v", err)
	}
	e, err := domain.NewEntry(domain.EntryParams{
		ID: id, Kind: domain.KindArticle, Title: title,
		Category: cat, Lifecycle: life, ReadState: &read, Tags: tags,
	})
	if err != nil {
		t.Fatalf("entry %d: %v", id, err)
	}
	return e
}

// withDescription пересобирает запись с описанием.
//
// Отдельно, потому что описание — единственное поле, где живет смысл записи:
// замер по живому каталогу дал 95 записей из 124 по запросу «контекст»,
// которые находятся ТОЛЬКО описанием. Витрина искала по нему всегда, терминал
// не искал никогда, и до #252 это расхождение было незаметно.
func withDescription(t *testing.T, e domain.Entry, desc string) domain.Entry {
	t.Helper()
	read := e.ReadState()
	out, err := domain.NewEntry(domain.EntryParams{
		ID: e.ID(), Kind: e.Kind(), Title: e.Title(), Description: desc,
		Category: e.Category(), Lifecycle: e.Lifecycle(), ReadState: read, Tags: e.Tags(),
	})
	if err != nil {
		t.Fatalf("entry with description: %v", err)
	}
	return out
}

func TestFilter_atUsecaseLevel(t *testing.T) {
	entries := []domain.Entry{
		entry(t, 3, "Оркестрация контейнеров", "kubernetes"),
		withDescription(t, entry(t, 13, "Как читать чужой код"),
			"разбор постмортема: что мешало понять систему"),
		entry(t, 300, "Дневник рефакторинга"),
	}

	cases := []struct {
		name  string
		query string
		want  []int
	}{
		{"пустой запрос ничего не отсеивает", "", []int{3, 13, 300}},
		{"кириллицей по латинскому тегу", "кубернетес", []int{3}},
		{"слова соединяются через И", "оркестрация контейнеров", []int{3}},
		{"решетка адресует ровно один id", "#3", []int{3}},
		{"ничего не найдено — пустой список", "телепортация", nil},
		{"описание тоже ищется", "постмортем", []int{13}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := search.Filter(entries, c.query)
			if len(got) != len(c.want) {
				t.Fatalf("%q: получено %d записей, ожидалось %d", c.query, len(got), len(c.want))
			}
			for i, id := range c.want {
				if got[i].ID() != id {
					t.Errorf("%q: позиция %d — id %d, ожидался %d", c.query, i, got[i].ID(), id)
				}
			}
		})
	}
}

// Выдача — по силе совпадения, а не по номеру записи: слово в названии выше
// слова в метке, метка выше описания, целое слово выше начала слова («SLA» —
// не «Slack»). Равные — в порядке каталога. Протыкивание 06.11: статья с SLA
// в названии стояла восьмой из 19, потому что номер у нее больше.
func TestFilter_ranksByWhereTheWordIs(t *testing.T) {
	es := []domain.Entry{
		withDescription(t, entry(t, 1, "Платформа агентов"), "интеграция со Slack и почтой"),
		withDescription(t, entry(t, 2, "Поддержка клиентов"), "договоренность про SLA до инцидента"),
		entry(t, 3, "Метрики сервиса", "sla"),
		entry(t, 4, "Между нами SLA: бизнес и поддержка"),
		withDescription(t, entry(t, 5, "Еще одна поддержка"), "тоже про SLA"),
	}
	cases := []struct {
		query string
		want  []int
	}{
		{"SLA", []int{4, 3, 2, 5, 1}},
		{"#3", []int{3}},
	}
	for _, tc := range cases {
		var got []int
		for _, e := range search.Filter(es, tc.query) {
			got = append(got, e.ID())
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q: %v, want %v", tc.query, got, tc.want)
		}
	}
}
