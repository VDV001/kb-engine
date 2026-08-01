package domain_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// A drift scan answers three different questions, and collapsing them loses the
// one that matters. 404 is a fact about the article. 403 is not: habr answers it
// both to a withdrawn article and to a bot it declines to serve, and all 49
// entries carrying 403 in the live catalog are habr. Calling those "dead" would
// bury 49 possibly-alive articles; calling them "alive" would hide withdrawals.
func TestClassifyLinkStatus(t *testing.T) {
	cases := []struct {
		name string
		code int
		want string
	}{
		{name: "ok", code: 200, want: "alive"},
		{name: "no content is still a live URL", code: 204, want: "alive"},
		{name: "permanent redirect", code: 301, want: "moved"},
		{name: "temporary redirect", code: 302, want: "moved"},
		{name: "not found", code: 404, want: "gone"},
		{name: "gone", code: 410, want: "gone"},
		{name: "forbidden is undecidable without a browser", code: 403, want: "undecidable"},
		{name: "rate limited says nothing about the article", code: 429, want: "undecidable"},
		{name: "server error says nothing about the article", code: 500, want: "undecidable"},
		{name: "gateway timeout", code: 504, want: "undecidable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := domain.ClassifyLinkStatus(c.code)
			if err != nil {
				t.Fatalf("ClassifyLinkStatus(%d): %v", c.code, err)
			}
			if got.String() != c.want {
				t.Fatalf("ClassifyLinkStatus(%d) = %q, want %q", c.code, got, c.want)
			}
		})
	}
}

func TestClassifyLinkStatusRejectsNonHTTPCode(t *testing.T) {
	for _, code := range []int{0, -1, 99, 600} {
		if _, err := domain.ClassifyLinkStatus(code); err == nil {
			t.Errorf("ClassifyLinkStatus(%d) accepted a non-HTTP code", code)
		}
	}
}

// Only «gone» is a verdict the owner can act on without opening a browser.
func TestLinkStatusIsActionable(t *testing.T) {
	cases := map[string]bool{"alive": false, "moved": false, "gone": true, "undecidable": false}
	for raw, want := range cases {
		s, err := domain.NewLinkStatus(raw)
		if err != nil {
			t.Fatalf("NewLinkStatus(%q): %v", raw, err)
		}
		if got := s.IsActionable(); got != want {
			t.Errorf("%q.IsActionable() = %v, want %v", raw, got, want)
		}
	}
}
