package cdpbrowser

import (
	_ "embed"
	"strconv"
	"strings"
)

//go:embed viewer.html
var viewerHTML string

// Viewer is the screen page for a w×h browser. Его отдает окно (а не сервер
// браузера) со своими заголовками безопасности: страница ходит по относительным
// адресам frame и input.
func Viewer(w, h int) string {
	return strings.NewReplacer("{{W}}", strconv.Itoa(w), "{{H}}", strconv.Itoa(h)).Replace(viewerHTML)
}
