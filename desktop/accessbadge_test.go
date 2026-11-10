package main

import (
	"os"
	"regexp"
	"testing"
)

// Плашка среды («стенд», «прод») в списке доступов — элемент сетки .ac-acc, а
// элементы сетки по умолчанию растягиваются на высоту строки: когда длинный
// логин переносился на вторую строку, плашка вытягивалась в прямоугольник.
func TestAccessEnvBadgeDoesNotStretch(t *testing.T) {
	css, err := os.ReadFile("frontend/style.css")
	if err != nil {
		t.Fatal(err)
	}
	rule := regexp.MustCompile(`(?m)^\.ac-acc \.ac-env\s*\{[^}]*align-self:\s*start`)
	if !rule.Match(css) {
		t.Fatal("у .ac-acc .ac-env нет align-self: start: плашка растягивается по высоте строки")
	}
}
