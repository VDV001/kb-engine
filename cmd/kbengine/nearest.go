package main

// nearestName возвращает имя из names, отличающееся от want не более чем на две
// правки, и пустую строку, если такого нет.
//
// Порог два, а не один как у флагов: имена категорий длиннее и ошибаются в них
// иначе — `dev-practice` вместо `dev-practices`, `golang` вместо `go-lang`.
//
// ponytail: расстояние считается по всему списку, потому что категорий два
// десятка. На тысяче имён это заметно, и путь наверх известен — отсечь по
// разнице длин до подсчёта.
func nearestName(want string, names []string) string {
	best, bestDist := "", 3
	for _, n := range names {
		if d := editDistance(want, n); d < bestDist {
			best, bestDist = n, d
		}
	}
	return best
}
