package filesearch_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

func TestTokens(t *testing.T) {
	got := filesearch.Tokens("Елка, договоры и Kubernetes-кластер: v2 x")
	// Однобуквенное («и», «x») не индексируется: это шум, а не слово поиска.
	want := []string{"елка", "договоры", "kubernetes", "кластер", "v2"}
	if !slices.Equal(got, want) {
		t.Fatalf("Tokens = %q, want %q", got, want)
	}
}

func TestStem_RussianFormsMeet(t *testing.T) {
	// Формы одного слова обязаны сойтись в одну основу: без этого запрос
	// «договор» не находит файл, где написано «договора», а именно так
	// пишут по-русски.
	for _, group := range [][]string{
		{"договор", "договора", "договоры", "договоров", "договорами"},
		// «заявок» не входит: беглая гласная, потолок срезания окончаний.
		{"заявка", "заявки", "заявку", "заявкой"},
		{"оплата", "оплаты", "оплату", "оплатой"},
	} {
		base := filesearch.Stem(group[0])
		for _, w := range group[1:] {
			if got := filesearch.Stem(w); got != base {
				t.Errorf("Stem(%q) = %q, Stem(%q) = %q", w, got, group[0], base)
			}
		}
	}
}

func TestStem_KeepsShortAndLatin(t *testing.T) {
	for _, w := range []string{"кот", "api", "kubernetes", "v2"} {
		if got := filesearch.Stem(w); got != w {
			t.Errorf("Stem(%q) = %q, want unchanged", w, got)
		}
	}
}

func doc(t *testing.T, path string) domain.FileDoc {
	t.Helper()
	d, err := domain.NewFileDoc(path, 1, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func paths(hits []filesearch.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Doc.Path())
	}
	return out
}

func TestIndex_SearchFindsWordForms(t *testing.T) {
	ix := filesearch.NewIndex()
	ix.Add(doc(t, "/w/a.md"), "Подписали договора с поставщиком")
	ix.Add(doc(t, "/w/b.md"), "Заявка на оплату")
	ix.Add(doc(t, "/w/c.md"), "Ничего общего")

	if got := paths(ix.Search("договор", 10)); !slices.Equal(got, []string{"/w/a.md"}) {
		t.Fatalf("договор -> %q", got)
	}
}

func TestIndex_AllTermsRequired(t *testing.T) {
	ix := filesearch.NewIndex()
	ix.Add(doc(t, "/w/a.md"), "заявка поставщику")
	ix.Add(doc(t, "/w/b.md"), "заявка на оплату")

	if got := paths(ix.Search("заявки оплата", 10)); !slices.Equal(got, []string{"/w/b.md"}) {
		t.Fatalf("заявки оплата -> %q", got)
	}
}

func TestIndex_RanksMoreMentionsHigherAndLimits(t *testing.T) {
	ix := filesearch.NewIndex()
	ix.Add(doc(t, "/w/once.md"), "агент один раз и много прочих слов вокруг")
	ix.Add(doc(t, "/w/often.md"), "агент агенты агента")
	ix.Add(doc(t, "/w/twice.md"), "агент и агенты")

	got := paths(ix.Search("агент", 2))
	if !slices.Equal(got, []string{"/w/often.md", "/w/twice.md"}) {
		t.Fatalf("ранжирование/лимит: %q", got)
	}
}

func TestIndex_FileNameCounts(t *testing.T) {
	// Имя файла тоже текст: «бюджет ташкента» ищут по имени чаще, чем по
	// содержимому.
	ix := filesearch.NewIndex()
	ix.Add(doc(t, "/w/tashkent_budget.py"), "print(1)")
	if got := paths(ix.Search("budget", 10)); !slices.Equal(got, []string{"/w/tashkent_budget.py"}) {
		t.Fatalf("budget -> %q", got)
	}
}

func TestIndex_EmptyQuery(t *testing.T) {
	ix := filesearch.NewIndex()
	ix.Add(doc(t, "/w/a.md"), "текст")
	if hits := ix.Search("  , ", 10); len(hits) != 0 {
		t.Fatalf("пустой запрос дал %d", len(hits))
	}
}

func TestSnippet(t *testing.T) {
	text := "первая строка\nвторая строка\nтут Договоры подписаны\nчетвертая"
	got := filesearch.Snippet(text, "договор", 80)
	if !strings.Contains(got, "Договоры подписаны") {
		t.Fatalf("Snippet = %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("сниппет должен быть одной строкой: %q", got)
	}
	if got := filesearch.Snippet(text, "отсутствует", 80); got != "первая строка" {
		t.Fatalf("без совпадения ждем первую строку, получили %q", got)
	}
}

func TestSnippet_CentersOnMatchInLongLine(t *testing.T) {
	// Длинная строка (записи хроники бывают по 500 символов) обрезалась с
	// начала, и найденное слово в выдачу не попадало — замер на живой папке.
	line := strings.Repeat("слово ", 60) + "трансграничка подана " + strings.Repeat("хвост ", 60)
	got := filesearch.Snippet(line, "трансграничка", 80)
	if !strings.Contains(got, "трансграничка") {
		t.Fatalf("совпадение не попало в сниппет: %q", got)
	}
	if n := len([]rune(got)); n > 82 {
		t.Fatalf("сниппет длиннее предела: %d", n)
	}
}

type fakeSource struct {
	files   map[string]string
	fail    string
	walkErr error
}

func (f fakeSource) Walk(ctx context.Context, fn func(domain.FileDoc) error) error {
	if f.walkErr != nil {
		return f.walkErr
	}
	keys := make([]string, 0, len(f.files))
	for k := range f.files {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		d, err := domain.NewFileDoc(k, int64(len(f.files[k])), time.Time{})
		if err != nil {
			return err
		}
		if err := fn(d); err != nil {
			return err
		}
	}
	return nil
}

func (f fakeSource) ReadText(path string) (string, error) {
	if path == f.fail {
		return "", errors.New("boom")
	}
	return f.files[path], nil
}

func TestService_BuildAndSearch(t *testing.T) {
	src := fakeSource{files: map[string]string{
		"/w/a.md": "строка\nПодписали договора",
		"/w/b.md": "другое",
		"/w/c.md": "битый",
	}, fail: "/w/c.md"}
	svc := filesearch.NewService(src)

	st, err := svc.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Нечитаемый файл не роняет сборку, но и не теряется молча: он посчитан.
	if st.Indexed != 2 || st.Skipped != 1 {
		t.Fatalf("stats = %+v", st)
	}
	// И назван: окно показывает, какой файл и почему (кнопка «подробнее»).
	if len(st.SkippedFiles) != 1 || st.SkippedFiles[0].Path != "/w/c.md" || st.SkippedFiles[0].Err == nil {
		t.Fatalf("SkippedFiles = %+v", st.SkippedFiles)
	}

	hits, err := svc.Search("договор", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Doc.Path() != "/w/a.md" || hits[0].Snippet != "Подписали договора" {
		t.Fatalf("hits = %+v", hits)
	}
	if got := svc.Stats(); !reflect.DeepEqual(got, st) {
		t.Fatalf("Stats() = %+v, want %+v", got, st)
	}
	if text, err := svc.Read("/w/b.md"); err != nil || text != "другое" {
		t.Fatalf("Read = %q, %v", text, err)
	}
}

func TestService_SearchDuringRebuild(t *testing.T) {
	// Окно и MCP ищут, пока индекс пересобирается: гонка тут ловится -race.
	src := fakeSource{files: map[string]string{"/w/a.md": "договор"}}
	svc := filesearch.NewService(src)
	if _, err := svc.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 20 {
			if _, err := svc.Build(context.Background()); err != nil {
				t.Error(err)
			}
		}
	}()
	for range 20 {
		if _, err := svc.Search("договор", 5); err != nil {
			t.Fatal(err)
		}
	}
	<-done
}

func (f fakeSource) Check(path string) error {
	if _, ok := f.files[path]; !ok {
		return filesearch.ErrNotAllowed
	}
	return nil
}

func TestService_FailedBuildIsNotStillBuilding(t *testing.T) {
	// Упавшая сборка отвечала «еще собирается» вечно, и окно висело на экране
	// индексации (ревью #371). Причина обязана дойти до того, кто спрашивает.
	svc := filesearch.NewService(fakeSource{walkErr: errors.New("disk gone")})
	if _, err := svc.Build(context.Background()); err == nil {
		t.Fatal("ждали ошибку сборки")
	}
	_, err := svc.Search("x", 5)
	if !errors.Is(err, filesearch.ErrBuildFailed) || errors.Is(err, filesearch.ErrNotBuilt) {
		t.Fatalf("Search err = %v, want ErrBuildFailed", err)
	}
	if !strings.Contains(err.Error(), "disk gone") {
		t.Fatalf("причина потеряна: %v", err)
	}
}

func TestService_CheckDelegatesToSource(t *testing.T) {
	svc := filesearch.NewService(fakeSource{files: map[string]string{"/w/a.md": "x"}})
	if err := svc.Check("/w/a.md"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Check("/w/.env"); !errors.Is(err, filesearch.ErrNotAllowed) {
		t.Fatalf("Check(.env) = %v", err)
	}
}

func TestService_ReplaceSwapsSourceAndIndex(t *testing.T) {
	// Владелец меняет папки в окне: новый индекс собирается с новым источником,
	// а до конца сборки поиск отвечает по старому — окно не пустеет.
	svc := filesearch.NewService(fakeSource{files: map[string]string{"/old/a.md": "договор"}})
	if _, err := svc.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := svc.Replace(context.Background(), fakeSource{files: map[string]string{"/new/b.md": "договор поставки"}})
	if err != nil || st.Indexed != 1 {
		t.Fatalf("Replace = %+v, %v", st, err)
	}
	hits, err := svc.Search("договор", 5)
	if err != nil || len(hits) != 1 || hits[0].Doc.Path() != "/new/b.md" {
		t.Fatalf("после Replace hits = %+v, %v", hits, err)
	}
	// Чтение и проверка идут через новый источник: старой папки больше нет.
	if err := svc.Check("/old/a.md"); !errors.Is(err, filesearch.ErrNotAllowed) {
		t.Fatalf("Check(old) = %v", err)
	}
	if _, err := svc.Read("/new/b.md"); err != nil {
		t.Fatalf("Read(new) = %v", err)
	}
}

func TestService_FailedReplaceKeepsOldIndex(t *testing.T) {
	svc := filesearch.NewService(fakeSource{files: map[string]string{"/old/a.md": "договор"}})
	if _, err := svc.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Replace(context.Background(), fakeSource{walkErr: errors.New("disk gone")}); err == nil {
		t.Fatal("ждали ошибку")
	}
	if hits, err := svc.Search("договор", 5); err != nil || len(hits) != 1 {
		t.Fatalf("старый индекс потерян: %+v, %v", hits, err)
	}
}

func TestService_FailedReplaceLeavesNoStaleError(t *testing.T) {
	// Пока старый индекс отвечает, ошибки «индекса нет» быть не должно.
	svc := filesearch.NewService(fakeSource{files: map[string]string{"/old/a.md": "договор"}})
	if _, err := svc.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _ = svc.Replace(context.Background(), fakeSource{walkErr: errors.New("disk gone")})
	if _, err := svc.Search("договор", 5); err != nil {
		t.Fatalf("Search после неудачной пересборки: %v", err)
	}
}

func TestIndex_SkipsHashLikeTokens(t *testing.T) {
	// Хеши, UUID и base64 в выгрузках раздували словарь индекса: каждое такое
	// «слово» уникально и в поиске не участвует. Обычные числа и версии
	// (2026, v2, 152-фз) остаются.
	ix := filesearch.NewIndex()
	ix.Add(doc(t, "/w/a.json"), `"id":"5e85d219-6ac2-4358-9a77-6591ab11ebb7" sha 3f9a8c1d2e4b5a6978c0d1e2f3a4b5c6 token eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9 версия v2 год 2026 закон 152`)
	for _, q := range []string{"6591ab11ebb7", "3f9a8c1d2e4b5a6978c0d1e2f3a4b5c6", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"} {
		if hits := ix.Search(q, 5); len(hits) != 0 {
			t.Errorf("%q проиндексирован", q)
		}
	}
	for _, q := range []string{"v2", "2026", "152", "версия"} {
		if hits := ix.Search(q, 5); len(hits) != 1 {
			t.Errorf("%q потерян", q)
		}
	}
	// 12 слов текста (короткие смеси вроде 6ac2 остаются) плюс json из имени файла.
	if n := ix.Terms(); n > 13 {
		t.Errorf("словарь %d основ, ждали не больше 13", n)
	}
}
