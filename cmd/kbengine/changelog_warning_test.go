package main

import (
	"strings"
	"testing"
)

// Флаг --changelog ждёт CHANGELOG.md, а рядом с каталогом лежит changelog.json —
// готовый разбор того же файла. Подсунуть второй вместо первого естественно, и
// именно это и произошло на живом запуске: парсер markdown не нашёл ни одного
// заголовка, отдал пустой документ, и на странице встало «v0.0.0 · —».
// Ноль вместо ошибки — худший из возможных ответов: он выглядит как факт.
func TestChangelogWarning(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		releases int
		want     string // подстрока, которую обязано содержать предупреждение
	}{
		{
			name:     "разобранный markdown молчит",
			path:     "/kb/CHANGELOG.md",
			releases: 29,
			want:     "",
		},
		{
			name:     "json вместо markdown называет причину",
			path:     "/kb/_data/changelog.json",
			releases: 0,
			want:     "CHANGELOG.md",
		},
		{
			name:     "пустой разбор md всё равно предупреждает",
			path:     "/kb/CHANGELOG.md",
			releases: 0,
			want:     "ни одного релиза",
		},
		{
			name:     "json, который всё же разобрался, не трогаем",
			path:     "/kb/changelog.json",
			releases: 3,
			want:     "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := changelogWarning(tc.path, tc.releases)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("changelogWarning(%q, %d) = %q, want empty", tc.path, tc.releases, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("changelogWarning(%q, %d) = %q, want it to mention %q",
					tc.path, tc.releases, got, tc.want)
			}
			// Предупреждение без пути бесполезно: оно должно показывать, какой
			// именно файл движок прочитал и не понял.
			if !strings.Contains(got, tc.path) {
				t.Errorf("предупреждение не называет путь: %q", got)
			}
		})
	}
}
