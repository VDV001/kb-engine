package httpapi

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// handleLlmsTxt отдаёт /llms.txt — машиночитаемую карту базы для агентов и
// LLM-поисковиков (спецификация llms.txt: markdown со стабильными секциями).
//
// Собирается ИЗ каталога через тот же Querier, что и витрина: добавили
// категорию — она появилась здесь без правки руками, второй копии перечня нет.
// Наружу уходит только публичное — категории и точки входа API, как и у /kb/;
// пути к файлам, личные заметки и финансы здесь не перечисляются.
func handleLlmsTxt(q Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		st, err := q.Stats()
		if err != nil {
			writeError(w, err)
			return
		}
		var b strings.Builder
		b.WriteString("# kb-engine\n\n")
		b.WriteString("> Персональная база знаний: каталог статей, стандартов и разборов ")
		b.WriteString("с поиском и финансовым учётом. Витрина только для чтения.\n\n")

		// Категории — по убыванию наполнения, затем по ключу: порядок
		// детерминированный, иначе один и тот же каталог давал бы разный файл.
		type cat struct {
			key, label string
			n          int
		}
		cats := make([]cat, 0, len(st.ByCategory))
		for k, n := range st.ByCategory {
			label := st.CategoryLabels[k]
			if label == "" {
				label = k
			}
			cats = append(cats, cat{k, label, n})
		}
		sort.Slice(cats, func(i, j int) bool {
			if cats[i].n != cats[j].n {
				return cats[i].n > cats[j].n
			}
			return cats[i].key < cats[j].key
		})

		b.WriteString("## Разделы каталога\n\n")
		if len(cats) == 0 {
			// «Разделов нет» — законная форма, а не ошибка: пустой каталог
			// остаётся валидным llms.txt, а не превращается в 500.
			b.WriteString("- (каталог пуст)\n")
		}
		for _, c := range cats {
			fmt.Fprintf(&b, "- %s — %d записей\n", c.label, c.n)
		}

		b.WriteString("\n## Точки входа\n\n")
		b.WriteString("- [Поиск](/api/search?q=) — полнотекстовый по каталогу, четыре слоя\n")
		b.WriteString("- [Все записи](/api/entries) — каталог целиком в JSON\n")
		b.WriteString("- [Статистика](/api/stats) — сводка по категориям и статусам\n")
		b.WriteString("- [Артефакты](/kb/) — собственные материалы базы по пути из каталога\n")

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(b.String()))
	}
}
