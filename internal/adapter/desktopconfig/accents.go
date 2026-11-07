package desktopconfig

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// ErrAccent — у оформления неизвестный режим или id акцента пустой, не того
// вида либо повторяется.
var ErrAccent = errors.New("оформление окна задано неверно")

// AccentsMode — какие акценты спектра окна включены.
type AccentsMode string

const (
	// AccentsMine — свой набор человека: все, кроме Off (по умолчанию).
	AccentsMine AccentsMode = ""
	// AccentsAll — все акценты; свой набор хранится.
	AccentsAll AccentsMode = "all"
	// AccentsNone — без акцентов; свой набор хранится.
	AccentsNone AccentsMode = "none"
)

// accentID — вид id акцента: его выбирает окно (tab-ink, pulse), Go список
// не знает — новые акценты не требуют правки здесь.
var accentID = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// Accents — оформление окна: режим и свой набор (выключенные в нем акценты).
// Пустой Off — все включены: новый акцент новой сборки появляется сам.
//
// Look — выбор значения у элемента, а не «вкл/выкл»: название сейфа, градиент,
// анимация (решение владельца 07.11: все до мелочей настраивается). Ключ — того
// же вида, что id акцента; значение — короткая строка, ее смысл знает окно.
type Accents struct {
	Mode AccentsMode       `json:"mode,omitempty"`
	Off  []string          `json:"off,omitempty"`
	Look map[string]string `json:"look,omitempty"`
}

// maxLookValue — длина значения выбора в знаках: название на табличке, имя градиента.
const maxLookValue = 40

func badLook(k, v string) bool {
	return !accentID.MatchString(k) || strings.TrimSpace(v) == "" || utf8.RuneCountInString(v) > maxLookValue || strings.ContainsAny(v, "\n\r\t")
}

// Check validates the look the window sends.
func (a Accents) Check() error {
	if !slices.Contains([]AccentsMode{AccentsMine, AccentsAll, AccentsNone}, a.Mode) {
		return fmt.Errorf("%w: режим %q", ErrAccent, a.Mode)
	}
	for i, id := range a.Off {
		if !accentID.MatchString(id) {
			return fmt.Errorf("%w: %q", ErrAccent, id)
		}
		if slices.Contains(a.Off[:i], id) {
			return fmt.Errorf("%w: %q повторяется", ErrAccent, id)
		}
	}
	for k, v := range a.Look {
		if badLook(k, v) {
			return fmt.Errorf("%w: выбор %q = %q", ErrAccent, k, v)
		}
	}
	return nil
}

// dropBadAccents чинит оформление при чтении, как dropBadTabs: оформление —
// только вид окна и не должно гасить источники MCP. Неизвестный режим
// становится своим набором, испорченные id и повторы отбрасываются.
func (c *Config) dropBadAccents() {
	if c.Accents.Check() == nil {
		return
	}
	if !slices.Contains([]AccentsMode{AccentsAll, AccentsNone}, c.Accents.Mode) {
		c.Accents.Mode = AccentsMine
	}
	seen := make(map[string]bool, len(c.Accents.Off))
	c.Accents.Off = slices.DeleteFunc(c.Accents.Off, func(id string) bool {
		bad := !accentID.MatchString(id) || seen[id]
		seen[id] = true
		return bad
	})
	maps.DeleteFunc(c.Accents.Look, badLook)
}
