package sourcesearch_test

import (
	"slices"
	"strconv"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/sourcesearch"
)

type rec struct{ id, key string }

func key(r rec) string { return r.key }

// hits renders the merged list as "source:id" for a compact comparison.
func hits(h []sourcesearch.Hit[rec]) []string {
	out := make([]string, len(h))
	for i, x := range h {
		out[i] = x.Source + ":" + x.Item.id
	}
	return out
}

func TestMerge(t *testing.T) {
	tests := []struct {
		name  string
		found []sourcesearch.Found[rec]
		want  []string
	}{
		{
			// Оценки у поиска по каталогу нет, все совпадения равны — первым идет
			// источник выше в списке, внутри источника — его собственный порядок.
			name: "порядок источников, внутри — порядок источника",
			found: []sourcesearch.Found[rec]{
				{Source: "server", Items: []rec{{"s2", "b"}, {"s1", "a"}}},
				{Source: "catalog", Items: []rec{{"c1", "c"}}},
			},
			want: []string{"server:s2", "server:s1", "catalog:c1"},
		},
		{
			// Одна запись на сервере и в каталоге — остается источник выше.
			name: "дубль остается у источника выше",
			found: []sourcesearch.Found[rec]{
				{Source: "catalog", Items: []rec{{"c1", "same"}, {"c2", "x"}}},
				{Source: "server", Items: []rec{{"s1", "same"}, {"s2", "y"}}},
			},
			want: []string{"catalog:c1", "catalog:c2", "server:s2"},
		},
		{
			// Пустой ключ — «опознать нельзя», а не «одна и та же запись»:
			// склеить такие значило бы потерять настоящие записи.
			name: "пустой ключ не склеивается",
			found: []sourcesearch.Found[rec]{
				{Source: "a", Items: []rec{{"a1", ""}}},
				{Source: "b", Items: []rec{{"b1", ""}}},
			},
			want: []string{"a:a1", "b:b1"},
		},
		{
			name: "источник без находок не мешает",
			found: []sourcesearch.Found[rec]{
				{Source: "a"},
				{Source: "b", Items: []rec{{"b1", "k"}}},
			},
			want: []string{"b:b1"},
		},
		{
			// Склеиваются дубли МЕЖДУ источниками; две записи одного каталога с
			// одним адресом — его дело (для них есть аудит дублей), выдача их не
			// прячет, как не прятал /api/search.
			name: "внутри одного источника не склеивается",
			found: []sourcesearch.Found[rec]{
				{Source: "catalog", Items: []rec{{"c1", "same"}, {"c2", "same"}}},
				{Source: "server", Items: []rec{{"s1", "same"}}},
			},
			want: []string{"catalog:c1", "catalog:c2"},
		},
		{name: "ничего", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hits(sourcesearch.Merge(tt.found, key)); !slices.Equal(got, tt.want) {
				t.Fatalf("Merge = %q, want %q", got, tt.want)
			}
		})
	}
}

// Ключ записи базы: адрес статьи, если он есть, иначе номер с заголовком.
// Номер в одиночку не годится — у двух разных каталогов номера совпадают.
func TestEntryKey(t *testing.T) {
	tests := []struct {
		name     string
		a, b     [3]string // id, title, url
		wantSame bool
	}{
		{"один адрес — одна запись", [3]string{"1", "Статья", "https://habr.com/p/1"}, [3]string{"9", "Статья (копия)", "https://habr.com/p/1"}, true},
		{"разные адреса", [3]string{"1", "Статья", "https://habr.com/p/1"}, [3]string{"1", "Статья", "https://habr.com/p/2"}, false},
		{"без адреса: номер и заголовок", [3]string{"5", "Заметка", ""}, [3]string{"5", "Заметка", ""}, true},
		{"без адреса: тот же номер, другой заголовок", [3]string{"5", "Заметка", ""}, [3]string{"5", "Другая", ""}, false},
		// Одна статья, записанная по-разному: схема, регистр хоста, косая черта
		// в конце, якорь и метки рассылки статью не меняют.
		{"http и https", [3]string{"1", "a", "http://habr.com/ru/articles/123/"}, [3]string{"2", "b", "https://habr.com/ru/articles/123/"}, true},
		{"косая черта в конце", [3]string{"1", "a", "https://habr.com/ru/articles/123/"}, [3]string{"2", "b", "https://habr.com/ru/articles/123"}, true},
		{"регистр хоста", [3]string{"1", "a", "https://Habr.COM/ru/articles/123"}, [3]string{"2", "b", "https://habr.com/ru/articles/123"}, true},
		{"якорь и utm", [3]string{"1", "a", "https://habr.com/ru/articles/123/?utm_source=tg#comments"}, [3]string{"2", "b", "https://habr.com/ru/articles/123"}, true},
		// Параметр, который выбирает страницу, — другая статья.
		{"другой параметр — другая статья", [3]string{"1", "a", "https://example.com/view?id=1"}, [3]string{"1", "a", "https://example.com/view?id=2"}, false},
		// Метки рекламных систем — по правилу домена (StripTrackingParams), а не
		// по второму списку здесь.
		{"yclid и fbclid", [3]string{"1", "a", "https://habr.com/ru/articles/1/?yclid=123&fbclid=x"}, [3]string{"2", "b", "https://habr.com/ru/articles/1/"}, true},
		// Якорь маршрутизации (#/…, #!/…) выбирает страницу; простой якорь — нет.
		{"маршрут в якоре", [3]string{"1", "a", "https://site.example/#/a"}, [3]string{"1", "a", "https://site.example/#/b"}, false},
		{"маршрут #! в якоре", [3]string{"1", "a", "https://site.example/#!/a"}, [3]string{"1", "a", "https://site.example/#!/b"}, false},
		// Регистр имени метки не меняет статью: UTM_Source — та же метка.
		{"UTM в верхнем регистре", [3]string{"1", "a", "https://habr.com/ru/articles/1/?UTM_Source=tg"}, [3]string{"2", "b", "https://habr.com/ru/articles/1/"}, true},
		// Пробел вокруг адреса — не другая статья.
		{"пробел вокруг адреса", [3]string{"1", "a", " https://habr.com/ru/articles/1/ "}, [3]string{"2", "b", "https://habr.com/ru/articles/1"}, true},
		{"регистр пути различается", [3]string{"1", "a", "https://example.com/A"}, [3]string{"1", "a", "https://example.com/a"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ka := sourcesearch.EntryKey(atoi(tt.a[0]), tt.a[1], tt.a[2])
			kb := sourcesearch.EntryKey(atoi(tt.b[0]), tt.b[1], tt.b[2])
			if ka == "" || (ka == kb) != tt.wantSame {
				t.Fatalf("keys %q и %q, want same=%v", ka, kb, tt.wantSame)
			}
		})
	}
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
