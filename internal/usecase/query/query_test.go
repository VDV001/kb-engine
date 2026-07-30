package query_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/query"
)

type fakeLoader struct {
	catalog *domain.Catalog
	err     error
}

func (f fakeLoader) Load() (*domain.Catalog, error) { return f.catalog, f.err }

func article(t *testing.T, id int, category, lifecycle, verdict string) domain.Entry {
	t.Helper()
	habrID := id
	rs, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	cat, err := domain.NewCategory(category)
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle(lifecycle)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	v, err := domain.NewVerdict(verdict)
	if err != nil {
		t.Fatalf("verdict: %v", err)
	}
	e, err := domain.NewEntry(domain.EntryParams{
		ID: id, Kind: "article", Title: "t", Category: cat, Lifecycle: lc,
		HabrID: &habrID, URL: "https://h/x", ReadState: &rs, Verdict: &v,
	})
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	return e
}

func TestStats(t *testing.T) {
	cat, err := domain.NewCatalog([]domain.Entry{
		article(t, 1, "golang", "active", "keep"),
		article(t, 2, "golang", "active", "consider"),
		article(t, 3, "management", "canonical", "keep"),
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	st, err := query.NewService(fakeLoader{catalog: cat}).Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Total != 3 {
		t.Errorf("Total = %d, want 3", st.Total)
	}
	if st.ByCategory["golang"] != 2 || st.ByCategory["management"] != 1 {
		t.Errorf("ByCategory = %v", st.ByCategory)
	}
	if st.ByLifecycle["active"] != 2 || st.ByLifecycle["canonical"] != 1 {
		t.Errorf("ByLifecycle = %v", st.ByLifecycle)
	}
	if st.ByVerdict["keep"] != 2 || st.ByVerdict["consider"] != 1 {
		t.Errorf("ByVerdict = %v", st.ByVerdict)
	}
	if st.ByKind["article"] != 3 {
		t.Errorf("ByKind = %v", st.ByKind)
	}
}

func TestEntries(t *testing.T) {
	cat, err := domain.NewCatalog([]domain.Entry{article(t, 1, "golang", "active", "keep")})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	entries, err := query.NewService(fakeLoader{catalog: cat}).Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 || entries[0].ID() != 1 {
		t.Errorf("entries = %v", entries)
	}
}

func TestStats_loaderError(t *testing.T) {
	sentinel := errors.New("boom")
	if _, err := query.NewService(fakeLoader{err: sentinel}).Stats(); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}

// Здоровье базы — две доли, и у них РАЗНЫЕ знаменатели, потому что это разные
// вопросы. «Разобрано» — доля от всего каталога: триаж применим к каждой
// записи. «С конспектом» — доля от разобранных СТАТЕЙ, потому что конспект к
// непрочитанной статье невозможен, а собственные материалы владельца несут
// путь к файлу по определению: файл и есть сам материал, а не разбор чужого.
//
// На живом каталоге это видно прямо: восемь записей-творений, и у всех восьми
// поле file заполнено. Считать их «конспектами» значит льстить метрике на
// ровном месте.
//
// Единого усреднённого числа здесь больше нет. Две доли несоизмеримы: 88% и
// 3% в среднем дают «здоровье 45%», что говорит неправду о базе, в которой
// разобрано почти всё. Полосу рисует главная ось — разобранность.
func TestService_health(t *testing.T) {
	entries := []domain.Entry{
		health(t, 1, "keep", "read", "notes/a.md"),
		health(t, 2, "skip", "read", ""),
		health(t, 3, "", "read", ""),
		health(t, 4, "", "unread", "notes/b.md"), // конспект к непрочитанному — не бывает, но данные всякие
		creation(t, 5, "draft", "standards/x.md"),
	}
	cat, err := domain.NewCatalog(entries)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	h, err := query.NewService(fakeLoader{catalog: cat}).Health()
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.Total != 5 {
		t.Errorf("Total = %d, want 5", h.Total)
	}
	if h.Processed != 3 {
		t.Errorf("Processed = %d, want 3 (три статьи с вердиктом или прочитанные)", h.Processed)
	}
	// Знаменатель второй доли: разобранные статьи, без творений.
	if h.NotesBase != 3 {
		t.Errorf("NotesBase = %d, want 3", h.NotesBase)
	}
	// Числитель: конспект у разобранной статьи. Ни творение с его собственным
	// файлом, ни непрочитанное сюда не попадают.
	if h.WithNotes != 1 {
		t.Errorf("WithNotes = %d, want 1", h.WithNotes)
	}
}

func TestService_health_emptyCatalog(t *testing.T) {
	cat, err := domain.NewCatalog(nil)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	h, err := query.NewService(fakeLoader{catalog: cat}).Health()
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.Total != 0 || h.Processed != 0 || h.NotesBase != 0 || h.WithNotes != 0 {
		t.Errorf("health = %+v, want zeroes", h)
	}
}

// creation — собственный материал владельца: у него стадия публикации вместо
// триажа, и путь к файлу есть всегда.
func creation(t *testing.T, id int, stage, notesFile string) domain.Entry {
	t.Helper()
	cat, err := domain.NewCategory("golang")
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	ps, err := domain.NewPublishStage(stage)
	if err != nil {
		t.Fatalf("publish stage: %v", err)
	}
	e, err := domain.NewEntry(domain.EntryParams{
		ID: id, Kind: domain.KindCreation, Title: "t", Category: cat, Lifecycle: lc,
		PublishStage: &ps, NotesFile: notesFile,
	})
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	return e
}

// health строит запись с нужными для этой карточки полями: вердикт может
// отсутствовать, конспект тоже.
func health(t *testing.T, id int, verdict, readState, notesFile string) domain.Entry {
	t.Helper()
	cat, err := domain.NewCategory("golang")
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	lc, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	p := domain.EntryParams{
		ID: id, Kind: "article", Title: "t", Category: cat, Lifecycle: lc,
		NotesFile: notesFile,
	}
	if verdict != "" {
		v, err := domain.NewVerdict(verdict)
		if err != nil {
			t.Fatalf("verdict: %v", err)
		}
		p.Verdict = &v
	}
	if readState != "" {
		rs, err := domain.NewReadState(readState)
		if err != nil {
			t.Fatalf("read state: %v", err)
		}
		p.ReadState = &rs
	}
	e, err := domain.NewEntry(p)
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	return e
}
