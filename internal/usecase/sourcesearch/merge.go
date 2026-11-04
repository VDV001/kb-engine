// Package sourcesearch joins what several sources found into one list: the
// window's results and, later, the MCP tools read one rule — источник выше в
// списке настроек побеждает, а одна запись в двух источниках показывается раз.
package sourcesearch

import "strconv"

// Found is what one source returned, in that source's order.
type Found[T any] struct {
	Source string
	Items  []T
}

// Hit is one item of the merged list with the source it came from.
type Hit[T any] struct {
	Source string
	Item   T
}

// Merge joins lists given in priority order. An item whose key was already
// seen is dropped, so a duplicate stays with the higher source; an empty key
// means «опознать нельзя» and is never merged.
//
// ponytail: поиск по каталогу — фильтр без оценки, все совпадения равны, и
// правило владельца «по релевантности, при равенстве — источник выше» сводится
// к порядку источников. Потолок: запись сервера ниже всех записей каталога,
// как бы точно она ни совпала. Путь: когда /api/search начнет отдавать оценку,
// сливать по ней, а приоритет оставить для равных.
func Merge[T any](found []Found[T], key func(T) string) []Hit[T] {
	seen := map[string]bool{}
	out := []Hit[T]{}
	for _, f := range found {
		for _, it := range f.Items {
			k := key(it)
			if k != "" {
				if seen[k] {
					continue
				}
				seen[k] = true
			}
			out = append(out, Hit[T]{Source: f.Source, Item: it})
		}
	}
	return out
}

// EntryKey identifies a knowledge-base entry across sources: its article
// address, or, without one, its number with its title — номера двух разных
// каталогов совпадают, и по номеру одному склеились бы чужие записи.
func EntryKey(id int, title, url string) string {
	if url != "" {
		return "url " + url
	}
	return "id " + strconv.Itoa(id) + " " + title
}
