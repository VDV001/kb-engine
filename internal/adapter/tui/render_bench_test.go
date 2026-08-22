package tui_test

import (
	"fmt"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/tui"
	"github.com/daniil/kb-engine/internal/domain"
)

// Отрисовка происходит на КАЖДОЕ нажатие клавиши, поэтому здесь важна не
// столько абсолютная величина, сколько то, что она не начнёт расти. Отдельное
// правило движка — «отрисовка не читает файлы»: одно чтение книги стоит
// миллисекунды, и в цикле перерисовки им места нет.
func benchModel(b *testing.B, n int) tui.Model {
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
	entries := make([]domain.Entry, 0, n)
	for i := range n {
		e, err := domain.NewEntry(domain.EntryParams{
			ID: i + 1, Kind: domain.KindArticle,
			Title:       fmt.Sprintf("Запись %d про профилирование и замеры", i+1),
			Description: "Разбор темы: устройство, замеры, где ломается",
			Category:    cat, Lifecycle: life, ReadState: &read,
			Tags: []string{"go", "профилирование"},
		})
		if err != nil {
			b.Fatal(err)
		}
		entries = append(entries, e)
	}
	return tui.NewModel(entries)
}

func BenchmarkView(b *testing.B) {
	for _, n := range []int{100, 1500} {
		m := benchModel(b, n)
		b.Run(fmt.Sprintf("записей_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = m.View()
			}
		})
	}
}
