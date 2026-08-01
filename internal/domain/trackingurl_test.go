package domain_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// The catalog carries 80 urls with utm_ tails picked up from an RSS digest.
// They are somebody else's campaign bookkeeping stored as if it were the
// address of the article.
func TestStripTrackingParams(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "utm tail from the rss digest",
			in:   "https://habr.com/ru/articles/1050292/?utm_campaign=1050292&utm_source=habrahabr&utm_medium=rss",
			want: "https://habr.com/ru/articles/1050292/",
		},
		{
			name: "every known tracker",
			in:   "https://example.com/a?fbclid=1&gclid=2&yclid=3&igshid=4&_openstat=5",
			want: "https://example.com/a",
		},
		{
			name: "a meaningful parameter survives",
			in:   "https://example.com/search?q=go&utm_source=rss",
			want: "https://example.com/search?q=go",
		},
		{
			name: "unknown parameter is left alone — guessing would lose real data",
			in:   "https://stitch.withgoogle.com/projects/713?pli=1",
			want: "https://stitch.withgoogle.com/projects/713?pli=1",
		},
		{name: "no query at all", in: "https://example.com/a", want: "https://example.com/a"},
		{name: "fragment survives", in: "https://example.com/a?utm_source=x#section", want: "https://example.com/a#section"},
		{name: "empty", in: "", want: ""},
		{name: "not a url is returned unchanged", in: "not a url", want: "not a url"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := domain.StripTrackingParams(c.in); got != c.want {
				t.Fatalf("StripTrackingParams(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// Order must be preserved for the parameters that stay: rewriting a url is a
// change to what the entry IS, and a reshuffled query would show up as a diff
// on entries nothing actually happened to.
func TestStripTrackingParams_keepsRemainingOrder(t *testing.T) {
	in := "https://example.com/a?b=2&utm_source=x&a=1"
	if got := domain.StripTrackingParams(in); got != "https://example.com/a?b=2&a=1" {
		t.Fatalf("got %q, want the surviving parameters in their original order", got)
	}
}
