package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestNewExternalURL(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		wantErr   bool
	}{
		{"http", "http://habr.com/ru/articles/1/", false},
		{"https", "https://habr.com/ru/articles/1/", false},
		{"пробелы обрезаются", "  https://h/1/  ", false},
		{"путь к файлу", "notes/rescued/2.md", true},
		{"чужая схема", "ftp://h/x", true},
		{"без схемы", "habr.com/ru/articles/2/", true},
		{"без хоста", "https:///path", true},
		{"пусто", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.NewExternalURL(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q", tc.raw)
				}
				if !errors.Is(err, domain.ErrInvalidLocation) {
					t.Errorf("error does not wrap ErrInvalidLocation: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() == "" {
				t.Error("valid url came back empty")
			}
		})
	}
}

func TestNewNotesPath(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
		wantErr         bool
	}{
		{"обычный путь", "notes/rescued/791_x.md", "notes/rescued/791_x.md", false},
		{"нормализуется", "notes/./rescued/../rescued/791_x.md", "notes/rescued/791_x.md", false},
		{"абсолютный", "/etc/passwd", "", true},
		{"домашний", "~/secrets.md", "", true},
		{"наверх", "../secrets.md", "", true},
		{"наверх через середину", "notes/../../x.md", "", true},
		{"пусто", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.NewNotesPath(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q", tc.raw)
				}
				if !errors.Is(err, domain.ErrInvalidLocation) {
					t.Errorf("error does not wrap ErrInvalidLocation: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != tc.want {
				t.Errorf("path = %q, want %q", got.String(), tc.want)
			}
		})
	}
}
