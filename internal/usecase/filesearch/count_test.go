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

// Строка источников над выдачей: у каждой папки «N из M», где M — сколько ее
// файлов нашел запрос. Выдача файлов срезана до limit, счет — нет; граница
// папки та же, что у CountByRoot («/w/ab» не входит в «/w/a»).
func TestService_CountMatches(t *testing.T) {
	files := map[string]string{"/w/ab/x.md": "агент", "/v/y.md": "агент", "/v/z.md": "другое"}
	for i := range 70 {
		files["/w/a/"+string(rune('a'+i%26))+string(rune('a'+i/26))+".md"] = "агент"
	}
	svc := filesearch.NewService(fakeSource{files: files})
	if _, err := svc.CountMatches("агент", []string{"/w/a"}); err == nil {
		t.Fatal("до сборки: нет ошибки")
	}
	if _, err := svc.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := svc.CountMatches("агент", []string{"/w/a", "/w/ab", "/v", "/gone"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"/w/a": 70, "/w/ab": 1, "/v": 1, "/gone": 0}; !maps.Equal(got, want) {
		t.Fatalf("CountMatches = %v, want %v", got, want)
	}
}
