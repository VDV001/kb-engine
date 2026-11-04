// Package sourcesearch joins what several sources found into one list: the
// window's results and, later, the MCP tools read one rule — источник выше в
// списке настроек побеждает, а одна запись в двух источниках показывается раз.
package sourcesearch

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

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

// Merge joins lists given in priority order. An item whose key a higher source
// already gave is dropped, so a duplicate stays with the higher source. Внутри
// одного источника ничего не склеивается: две записи каталога с одним адресом —
// его дело (для них аудит дублей), и /api/search их не прятал. Пустой ключ —
// «опознать нельзя», не склеивается никогда.
//
// ponytail: поиск по каталогу — фильтр без оценки, все совпадения равны, и
// правило владельца «по релевантности, при равенстве — источник выше» сводится
// к порядку источников. Потолок: запись сервера ниже всех записей каталога,
// как бы точно она ни совпала. Путь: когда /api/search начнет отдавать оценку,
// сливать по ней, а приоритет оставить для равных.
func Merge[T any](found []Found[T], key func(T) string) []Hit[T] {
	seen := map[string]bool{} // ключи источников выше текущего
	out := []Hit[T]{}
	for _, f := range found {
		mine := map[string]bool{}
		for _, it := range f.Items {
			k := key(it)
			if k != "" && seen[k] {
				continue
			}
			if k != "" {
				mine[k] = true
			}
			out = append(out, Hit[T]{Source: f.Source, Item: it})
		}
		for k := range mine {
			seen[k] = true
		}
	}
	return out
}

// EntryKey identifies a knowledge-base entry across sources: its article
// address, or, without one, its number with its title — номера двух разных
// каталогов совпадают, и по номеру одному склеились бы чужие записи.
func EntryKey(id int, title, address string) string {
	if address = strings.TrimSpace(address); address != "" {
		return "url " + canonicalURL(address)
	}
	return "id " + strconv.Itoa(id) + " " + title
}

// canonicalURL drops what does not change the article: схема http/https,
// регистр хоста, косая черта в конце пути, простой якорь и метки — по правилу
// домена StripTrackingParams, второго списка меток здесь нет. Путь, остальные
// параметры и якорь маршрутизации (#/…, #!/…) остаются: ?id=1 и ?id=2, #/a и
// #/b — разные статьи.
func canonicalURL(raw string) string {
	u, err := url.Parse(domain.StripTrackingParams(raw))
	if err != nil || u.Host == "" {
		return raw
	}
	key := strings.ToLower(u.Host) + strings.TrimRight(u.EscapedPath(), "/")
	if u.RawQuery != "" {
		key += "?" + u.Query().Encode() // Encode сортирует ключи
	}
	if strings.HasPrefix(u.Fragment, "/") || strings.HasPrefix(u.Fragment, "!") {
		key += "#" + u.Fragment
	}
	return key
}

// Split returns the first n items and the merge keys of the rest — то, что
// сервер отдает окну по limit: склейке нужны ключи всех найденных, а сами
// записи — только показанные. Ключи — всегда список, не nil.
func Split[T any](items []T, n int, key func(T) string) ([]T, []string) {
	n = max(0, min(n, len(items)))
	keys := make([]string, 0, len(items)-n)
	for _, it := range items[n:] {
		keys = append(keys, key(it))
	}
	return items[:n], keys
}
