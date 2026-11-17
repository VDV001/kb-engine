package domain_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// Подсказка модели правилами, без вызова модели (решение владельца 17.11, #678):
// сложное — основной, короткое и простое — второстепенной.
func TestSuggestModelRole(t *testing.T) {
	cases := []struct {
		name     string
		question string
		files    int
		role     domain.ModelRole
		reason   domain.HintReason
	}{
		{"empty", "  ", 0, "", ""},
		{"greeting", "привет", 0, domain.RoleSecond, domain.ReasonShort},
		{"short fact", "сколько стоит подписка?", 0, domain.RoleSecond, domain.ReasonShort},
		{"long", strings.Repeat("слово ", 100), 0, domain.RoleMain, domain.ReasonLong},
		{"code fence", "что делает ```go\nfunc f() {}\n```", 0, domain.RoleMain, domain.ReasonCode},
		{"analysis ru", "Сравни два договора и найди риски", 0, domain.RoleMain, domain.ReasonAnalysis},
		{"analysis case", "ПРОАНАЛИЗИРУЙ продажи", 0, domain.RoleMain, domain.ReasonAnalysis},
		{"files", "что тут?", 1, domain.RoleMain, domain.ReasonFiles},
		{"long beats short", strings.Repeat("а", 401), 0, domain.RoleMain, domain.ReasonLong},
	}
	for _, c := range cases {
		got := domain.SuggestModelRole(c.question, c.files)
		if got.Role != c.role || got.Reason != c.reason {
			t.Errorf("%s: %+v, want %s/%s", c.name, got, c.role, c.reason)
		}
	}
}
