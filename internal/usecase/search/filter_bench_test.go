package search_test

import (
	"fmt"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// Поиск переделан в четыре слоя (подстрока, транслитерация, расстояние правок,
// словарь), и до этих бенчмарков его цена не измерялась ни разу — регрессия по
// времени ответа была невидима.
//
// ⚠️ Записи выдуманные и однородные: это замер СЛОЁВ, а не живого каталога.
// На настоящих данных доля попаданий другая, и абсолютные числа отличаются.
func benchEntries(b *testing.B, n int) []domain.Entry {
	b.Helper()
	cat, err := domain.NewCategory("articles")
	if err != nil {
		b.Fatal(err)
	}
	life, err := domain.NewLifecycle("active")
	if err != nil {
		b.Fatal(err)
	}
	read, err := domain.NewReadState("read")
	if err != nil {
		b.Fatal(err)
	}
	out := make([]domain.Entry, 0, n)
	words := []string{"kubernetes", "докер", "промпт", "агент", "очередь", "postgres", "трассировка", "профилирование"}
	for i := range n {
		e, err := domain.NewEntry(domain.EntryParams{
			ID:          i + 1,
			Kind:        domain.KindArticle,
			Title:       fmt.Sprintf("Запись %d про %s", i+1, words[i%len(words)]),
			Description: fmt.Sprintf("Разбор темы %s: устройство, замеры, где ломается", words[(i+3)%len(words)]),
			Category:    cat, Lifecycle: life, ReadState: &read,
			Tags: []string{words[(i+1)%len(words)], words[(i+5)%len(words)]},
		})
		if err != nil {
			b.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// Три запроса берут разные слои: первый закрывается подстрокой, второй требует
// транслитерации, третий — расстояния правок. Одним запросом мерить нельзя:
// дешёвый слой отвечает первым и прячет цену остальных.
func BenchmarkFilter(b *testing.B) {
	entries := benchEntries(b, 1500)
	for _, q := range []struct {
		name, query string
	}{
		{"подстрока", "kubernetes"},
		{"транслитерация", "кубернетес"},
		{"опечатка", "kubernets"},
		{"два_слова", "промпт агент"},
	} {
		b.Run(q.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = search.Filter(entries, q.query)
			}
		})
	}
}
