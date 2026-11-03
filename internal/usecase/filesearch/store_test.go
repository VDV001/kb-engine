package filesearch_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

type file struct {
	text string
	mod  time.Time
}

// countingSource считает чтения: инкрементальная сборка обязана не читать
// файлы, которые не менялись.
type countingSource struct {
	files map[string]file
	reads *int
}

func (s countingSource) Walk(ctx context.Context, fn func(domain.FileDoc) error) error {
	keys := make([]string, 0, len(s.files))
	for k := range s.files {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		d, err := domain.NewFileDoc(k, int64(len(s.files[k].text)), s.files[k].mod)
		if err != nil {
			return err
		}
		if err := fn(d); err != nil {
			return err
		}
	}
	return nil
}

func (s countingSource) ReadText(path string) (string, error) {
	*s.reads++
	return s.files[path].text, nil
}

func (s countingSource) Check(string) error { return nil }

type memStore struct {
	snap  *filesearch.Snapshot
	saves int
}

func (m *memStore) Load() (filesearch.Snapshot, bool, error) {
	if m.snap == nil {
		return filesearch.Snapshot{}, false, nil
	}
	return *m.snap, true, nil
}

func (m *memStore) Save(s filesearch.Snapshot) error {
	m.snap = &s
	m.saves++
	return nil
}

// t0 заведомо в прошлом: файлы моложе прошлой сборки перечитываются (racy-clean).
var t0 = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

func TestService_RebuildReadsOnlyChangedFiles(t *testing.T) {
	store := &memStore{}
	reads := 0
	files := map[string]file{
		"/w/a.md": {"договор поставки", t0},
		"/w/b.md": {"заявка на оплату", t0},
		"/w/c.md": {"будет удален", t0},
	}
	src := countingSource{files: files, reads: &reads}

	// Первый запуск: снимка нет, читаются все файлы, снимок сохраняется.
	svc := filesearch.NewService(src, filesearch.WithStore(store))
	if _, err := svc.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reads != 3 || store.saves != 1 {
		t.Fatalf("первая сборка: reads=%d saves=%d", reads, store.saves)
	}

	// Второй запуск — новый процесс, индекс берется со «диска»: изменился b,
	// удален c, добавлен d. Читаются только b и d.
	files["/w/b.md"] = file{"заявка на оплату договора", t0.Add(time.Minute)}
	delete(files, "/w/c.md")
	files["/w/d.md"] = file{"новый договор", t0}
	reads = 0
	svc2 := filesearch.NewService(src, filesearch.WithStore(store))
	st, err := svc2.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reads != 2 || st.Reused != 1 || st.Indexed != 3 {
		t.Fatalf("инкремент: reads=%d stats=%+v", reads, st)
	}

	// Снимок отвечает ровно как свежая сборка: а (из снимка), b и d (свежие).
	hits, err := svc2.Search("договор", 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range hits {
		got = append(got, h.Doc.Path())
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"/w/a.md", "/w/b.md", "/w/d.md"}) {
		t.Fatalf("hits = %q", got)
	}
	if h, _ := svc2.Search("удален", 5); len(h) != 0 {
		t.Fatalf("удаленный файл остался в индексе: %+v", h)
	}
}

func TestService_InMemoryReplaceIsIncrementalToo(t *testing.T) {
	// Смена папок в окне: прежний индекс в памяти служит снимком, и общие
	// файлы не перечитываются.
	reads := 0
	files := map[string]file{"/w/a.md": {"договор", t0}}
	svc := filesearch.NewService(countingSource{files: files, reads: &reads})
	if _, err := svc.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	files["/w/b.md"] = file{"еще договор", t0}
	reads = 0
	st, err := svc.Replace(context.Background(), countingSource{files: files, reads: &reads})
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 || st.Reused != 1 {
		t.Fatalf("reads=%d stats=%+v", reads, st)
	}
}

func TestSnapshot_RoundTrip(t *testing.T) {
	ix := filesearch.NewIndex()
	d, err := domain.NewFileDoc("/w/a.md", 10, t0)
	if err != nil {
		t.Fatal(err)
	}
	ix.Add(d, "договор поставки")
	back, err := filesearch.IndexFromSnapshot(ix.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	hits := back.Search("договора", 5)
	if len(hits) != 1 || hits[0].Doc.Path() != "/w/a.md" || !hits[0].Doc.ModTime().Equal(t0) || hits[0].Doc.Size() != 10 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestIndexFromSnapshot_RejectsBrokenSnapshot(t *testing.T) {
	// Снимок с диска — внешние данные: битая ссылка на файл не должна уронить
	// поиск паникой, а относительный путь — пройти мимо NewFileDoc.
	for _, s := range []filesearch.Snapshot{
		{Version: filesearch.SnapshotVersion, Docs: []filesearch.DocRecord{{Path: "relative.md"}}},
		{Version: filesearch.SnapshotVersion, Docs: []filesearch.DocRecord{{Path: "/w/a.md"}}, Terms: []string{"x"}, Postings: [][]filesearch.Posting{{{Doc: 5, TF: 1}}}},
		{Version: filesearch.SnapshotVersion, Terms: []string{"x", "y"}, Postings: [][]filesearch.Posting{{}}},
		{Version: filesearch.SnapshotVersion + 1},
	} {
		if _, err := filesearch.IndexFromSnapshot(s); err == nil {
			t.Errorf("снимок %+v принят", s)
		}
	}
}

func TestIndex_IdentifiersStaySearchableInNamesAndQueries(t *testing.T) {
	// Отсев идентификаторов — только для содержимого: имя файла и запрос
	// человек пишет сам, и «invoice20261103» в имени обязан находиться.
	ix := filesearch.NewIndex()
	d, err := domain.NewFileDoc("/w/invoice20261103.md", 1, t0)
	if err != nil {
		t.Fatal(err)
	}
	ix.Add(d, "счет на оплату")
	if hits := ix.Search("invoice20261103", 5); len(hits) != 1 {
		t.Fatalf("имя с идентификатором не ищется: %d", len(hits))
	}
	if hits := ix.Search("оплату invoice20261103", 5); len(hits) != 1 {
		t.Fatalf("запрос с идентификатором потерял слово: %d", len(hits))
	}
}

func TestService_RacyCleanFileIsReread(t *testing.T) {
	// Файл, измененный в ту же секунду, что и прошлая сборка, мог быть
	// сохранен еще раз с тем же размером: доверять ему нельзя (как в git).
	store := &memStore{}
	reads := 0
	now := time.Now()
	files := map[string]file{"/w/a.md": {"старое", now}, "/w/b.md": {"давнее", now.Add(-time.Hour)}}
	src := countingSource{files: files, reads: &reads}
	if _, err := filesearch.NewService(src, filesearch.WithStore(store)).Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	reads = 0
	st, err := filesearch.NewService(src, filesearch.WithStore(store)).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 || st.Reused != 1 {
		t.Fatalf("свежий файл не перечитан: reads=%d stats=%+v", reads, st)
	}
}

func TestService_SnapshotFromOtherRulesIsIgnored(t *testing.T) {
	// Снимок, собранный другими правилами разбора слов, нельзя переиспользовать:
	// основы в нем не совпадут с основами запроса.
	store := &memStore{}
	reads := 0
	src := countingSource{files: map[string]file{"/w/a.md": {"договор", t0}}, reads: &reads}
	if _, err := filesearch.NewService(src, filesearch.WithStore(store)).Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.snap.Rules = "other"
	reads = 0
	st, err := filesearch.NewService(src, filesearch.WithStore(store)).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Reused != 0 || reads != 1 || st.Warning == "" {
		t.Fatalf("чужие правила: reads=%d stats=%+v", reads, st)
	}
}

func TestService_NoChangesNoSave(t *testing.T) {
	store := &memStore{}
	reads := 0
	src := countingSource{files: map[string]file{"/w/a.md": {"договор", t0}}, reads: &reads}
	for range 2 {
		if _, err := filesearch.NewService(src, filesearch.WithStore(store)).Build(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if store.saves != 1 {
		t.Fatalf("снимок без изменений переписан: saves=%d", store.saves)
	}
}

func TestService_SamePathTwiceIsIndexedOnce(t *testing.T) {
	// Пересекающиеся корни (~/git и ~/git/kb-engine) дают тот же путь дважды.
	reads := 0
	src := dupSource{countingSource{files: map[string]file{"/w/a.md": {"договор", t0}}, reads: &reads}}
	svc := filesearch.NewService(src)
	for range 2 {
		st, err := svc.Build(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if st.Indexed != 1 {
			t.Fatalf("stats = %+v", st)
		}
	}
}

type dupSource struct{ countingSource }

func (s dupSource) Walk(ctx context.Context, fn func(domain.FileDoc) error) error {
	for range 2 {
		if err := s.countingSource.Walk(ctx, fn); err != nil {
			return err
		}
	}
	return nil
}

func TestSnapshot_KeepsOldAndFarDates(t *testing.T) {
	for _, mod := range []time.Time{time.Date(1601, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2400, 1, 1, 0, 0, 0, 5, time.UTC)} {
		ix := filesearch.NewIndex()
		d, err := domain.NewFileDoc("/w/a.md", 1, mod)
		if err != nil {
			t.Fatal(err)
		}
		ix.Add(d, "x слово")
		back, err := filesearch.IndexFromSnapshot(ix.Snapshot())
		if err != nil {
			t.Fatal(err)
		}
		if got := back.Search("слово", 1)[0].Doc.ModTime(); !got.Equal(mod) {
			t.Errorf("дата %v вернулась как %v", mod, got)
		}
	}
}
