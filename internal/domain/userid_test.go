package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// Имя пользователя становится частью ключа хранилища (users/<id>/…), поэтому
// допустимо только то, что не может стать путем наружу или чужим каталогом.
func TestNewUserID(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"owner", true},
		{"u2", true},
		{"anna-2026", true},
		{"a", true},
		{"0123456789abcdef0123456789abcdef", true}, // 32
		{"", false},
		{"0123456789abcdef0123456789abcdef0", false}, // 33
		{"Owner", false},
		{"анна", false},
		{"../owner", false},
		{"a/b", false},
		{".", false},
		{"-lead", false},
		{"trail-", false},
		{"a_b", false},
		{"a b", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			id, err := domain.NewUserID(c.in)
			if (err == nil) != c.ok {
				t.Fatalf("NewUserID(%q): err = %v, want ok = %v", c.in, err, c.ok)
			}
			if !c.ok && !errors.Is(err, domain.ErrInvalidUserID) {
				t.Fatalf("err = %v, want ErrInvalidUserID", err)
			}
			if c.ok && id.String() != c.in {
				t.Fatalf("String() = %q, want %q", id.String(), c.in)
			}
		})
	}
}
