package filesearch_test

import (
	"context"
	"maps"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// Экран источников показывает у каждой папки, сколько файлов из нее в индексе.
// Счет по границе папки: «/w/ab» не входит в «/w/a».
func TestService_CountByRoot(t *testing.T) {
	svc := filesearch.NewService(fakeSource{files: map[string]string{
		"/w/a/1.md": "x", "/w/a/sub/2.md": "x", "/w/ab/3.md": "x", "/v/4.md": "x",
	}})
	if got := svc.CountByRoot([]string{"/w/a"}); got != nil {
		t.Fatalf("до сборки = %v, want nil", got)
	}
	if _, err := svc.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := svc.CountByRoot([]string{"/w/a", "/w/ab", "/v", "/gone"})
	want := map[string]int{"/w/a": 2, "/w/ab": 1, "/v": 1, "/gone": 0}
	if !maps.Equal(got, want) {
		t.Fatalf("CountByRoot = %v, want %v", got, want)
	}
}
