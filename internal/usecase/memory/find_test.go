package memory_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// fakeEmbedder кладет текст на две оси: деньги и здоровье. Третья координата
// не дает нулевого вектора, у которого нет косинуса.
type fakeEmbedder struct {
	calls  int
	failOn string // если текст содержит это, вернуть ошибку
	down   error  // эмбеддер недоступен целиком
}

var (
	moneyAxis  = regexp.MustCompile(`(?i)фин|ден|бюдж`)
	healthAxis = regexp.MustCompile(`(?i)здоров|врач`)
)

func (e *fakeEmbedder) Embed(text string) (search.Vector, error) {
	e.calls++
	if e.down != nil {
		return nil, e.down
	}
	if e.failOn != "" && strings.Contains(text, e.failOn) {
		return nil, errors.New("отказ на записи")
	}
	return search.Vector{
		float32(len(moneyAxis.FindAllString(text, -1))),
		float32(len(healthAxis.FindAllString(text, -1))),
		0.1,
	}, nil
}

func finNote() memory.NoteText {
	return memory.NoteText{
		Note: domain.ColdNote{File: "budget-rules.md", Name: "budget-rules", Topic: "финансы", Description: "бюджет и деньги по месяцам", Digest: "d-fin"},
		Body: "Расходы считаем без переводов себе.",
	}
}

func docNote() memory.NoteText {
	return memory.NoteText{
		Note: domain.ColdNote{File: "doctor.md", Name: "doctor", Topic: "здоровье", Description: "визит к врачу раз в год", Digest: "d-doc"},
		Body: "Записаться к врачу. Код PROD-0079.",
	}
}

func names(h []memory.Hit) []string {
	var out []string
	for _, x := range h {
		out = append(out, x.Note.Name)
	}
	return out
}

func newFind(t *testing.T, texts ...memory.NoteText) (*fakeStore, *memory.Service) {
	t.Helper()
	st := &fakeStore{texts: texts}
	return st, memory.New(st, memory.Limits{})
}

// Текстовый поиск работает из коробки: без эмбеддера и без индекса.
func TestFind_textWorksOutOfTheBox(t *testing.T) {
	_, svc := newFind(t, finNote(), docNote())
	res, err := svc.Find("финансовый бюджет", memory.FindOptions{})
	if err != nil || res.Via != "text" || len(res.Hits) != 1 || res.Hits[0].Note.Name != "budget-rules" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Notice != "" {
		t.Errorf("без эмбеддера это штатный режим, а не деградация: %q", res.Notice)
	}
}

func TestFind_noMatchIsEmptyNotError(t *testing.T) {
	_, svc := newFind(t, finNote(), docNote())
	res, err := svc.Find("квантовая гравитация", memory.FindOptions{})
	if err != nil || len(res.Hits) != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// Вектор идет первым: перефраз без общих слов с записью находится только им.
func TestFind_vectorFirst(t *testing.T) {
	emb := &fakeEmbedder{}
	st, svc := newFind(t, finNote(), docNote())
	svc = svc.WithEmbedder(emb, "bge-m3")
	if _, err := svc.BuildIndex(); err != nil {
		t.Fatal(err)
	}
	const q = "сколько денег уходит"
	if txt, _ := svc.Find(q, memory.FindOptions{Mode: memory.ModeText}); len(txt.Hits) != 0 {
		t.Fatalf("запрос выбран неудачно: текст его уже находит %v", names(txt.Hits))
	}
	res, err := svc.Find(q, memory.FindOptions{})
	if err != nil || res.Via != "vector" || len(res.Hits) != 1 || res.Hits[0].Note.Name != "budget-rules" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	_ = st
}

// Смесь двух слоев 1:1 хуже одного вектора (36/40 против 39/40, ADR-0008):
// когда вектор нашел, текст в выдачу не подмешивается.
func TestFind_vectorAnswerIsNotMixedWithText(t *testing.T) {
	emb := &fakeEmbedder{}
	_, svc := newFind(t, finNote(), docNote())
	svc = svc.WithEmbedder(emb, "bge-m3")
	_, _ = svc.BuildIndex()
	// «врач» есть в тексте doctor, но смысл запроса про деньги.
	res, _ := svc.Find("деньги врач", memory.FindOptions{Threshold: 0.5})
	for _, h := range res.Hits {
		if h.Via != "vector" {
			t.Errorf("в выдаче вектора оказался %q: %+v", h.Via, h)
		}
	}
}

// Точный идентификатор вектор не видит, его находит текст — запасной слой.
func TestFind_textFallbackForExactIdentifier(t *testing.T) {
	emb := &fakeEmbedder{}
	_, svc := newFind(t, finNote(), docNote())
	svc = svc.WithEmbedder(emb, "bge-m3")
	_, _ = svc.BuildIndex()
	res, err := svc.Find("PROD-0079", memory.FindOptions{})
	if err != nil || res.Via != "text" || len(res.Hits) != 1 || res.Hits[0].Note.Name != "doctor" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Notice, "ниже порога") {
		t.Errorf("переход на текст должен называться: %q", res.Notice)
	}
}

// Эмбеддер упал: ответ по тексту и причина вслух, а не пустота.
func TestFind_embedderDownFallsBackToTextAndSaysWhy(t *testing.T) {
	emb := &fakeEmbedder{}
	_, svc := newFind(t, finNote(), docNote())
	svc = svc.WithEmbedder(emb, "bge-m3")
	_, _ = svc.BuildIndex()
	emb.down = errors.New("connection refused")
	res, err := svc.Find("бюджет", memory.FindOptions{})
	if err != nil || res.Via != "text" || len(res.Hits) != 1 || !strings.Contains(res.Notice, "connection refused") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestFind_vectorModeNeedsTheLayer(t *testing.T) {
	_, svc := newFind(t, finNote())
	if _, err := svc.Find("бюджет", memory.FindOptions{Mode: memory.ModeVector}); !errors.Is(err, search.ErrNoSemanticLayer) {
		t.Fatalf("без эмбеддера err = %v, want ErrNoSemanticLayer", err)
	}
	svc = svc.WithEmbedder(&fakeEmbedder{}, "bge-m3") // индекс не снят
	if _, err := svc.Find("бюджет", memory.FindOptions{Mode: memory.ModeVector}); !errors.Is(err, search.ErrNoSemanticLayer) {
		t.Fatalf("без индекса err = %v, want ErrNoSemanticLayer", err)
	}
}

func TestFind_limitAndOneEntryPerNote(t *testing.T) {
	var many []memory.NoteText
	for _, n := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7"} {
		many = append(many, memory.NoteText{
			Note: domain.ColdNote{File: n + ".md", Name: n, Topic: "финансы", Description: "бюджет"},
			Body: "бюджет бюджет бюджет",
		})
	}
	_, svc := newFind(t, many...)
	res, _ := svc.Find("бюджет", memory.FindOptions{})
	if len(res.Hits) != 5 {
		t.Errorf("по умолчанию в пятерку, got %d", len(res.Hits))
	}
	res, _ = svc.Find("бюджет", memory.FindOptions{Limit: 2})
	seen := map[string]bool{}
	for _, h := range res.Hits {
		if seen[h.Note.Name] {
			t.Errorf("запись %s дважды", h.Note.Name)
		}
		seen[h.Note.Name] = true
	}
	if len(res.Hits) != 2 {
		t.Errorf("Limit 2, got %d", len(res.Hits))
	}
}

// Записи без вектора вектор не увидит: выдача говорит, сколько их.
func TestFind_noticeAboutNotesWithoutVectors(t *testing.T) {
	emb := &fakeEmbedder{}
	st, svc := newFind(t, finNote())
	svc = svc.WithEmbedder(emb, "bge-m3")
	_, _ = svc.BuildIndex()
	st.texts = append(st.texts, docNote()) // добавлена после снятия индекса
	res, _ := svc.Find("сколько денег уходит", memory.FindOptions{})
	if !strings.Contains(res.Notice, "1 запис") || !strings.Contains(res.Notice, "memory index") {
		t.Errorf("Notice = %q", res.Notice)
	}
}

func TestBuildIndex_reusesUnchangedAndEmbedsChanged(t *testing.T) {
	emb := &fakeEmbedder{}
	st, svc := newFind(t, finNote(), docNote())
	svc = svc.WithEmbedder(emb, "bge-m3")
	rep, err := svc.BuildIndex()
	if err != nil || rep.Embedded != 2 || rep.Reused != 0 {
		t.Fatalf("первый раз: %+v %v", rep, err)
	}
	emb.calls = 0
	rep, err = svc.BuildIndex()
	if err != nil || rep.Embedded != 0 || rep.Reused != 2 || emb.calls != 0 {
		t.Fatalf("без изменений: %+v calls=%d %v", rep, emb.calls, err)
	}
	st.texts[0].Note.Digest = "d-fin-2" // текст записи изменился
	rep, _ = svc.BuildIndex()
	if rep.Embedded != 1 || rep.Reused != 1 {
		t.Errorf("изменилась одна запись: %+v", rep)
	}
}

// Другая модель дает несравнимые векторы: старые переиспользовать нельзя.
func TestBuildIndex_otherModelRebuildsEverything(t *testing.T) {
	emb := &fakeEmbedder{}
	_, svc := newFind(t, finNote(), docNote())
	_, _ = svc.WithEmbedder(emb, "bge-m3").BuildIndex()
	rep, _ := svc.WithEmbedder(emb, "nomic").BuildIndex()
	if rep.Embedded != 2 || rep.Reused != 0 {
		t.Errorf("rep = %+v", rep)
	}
}

func TestBuildIndex_partialFailureIsCountedAndAllFailureIsAnError(t *testing.T) {
	emb := &fakeEmbedder{failOn: "врач"}
	st, svc := newFind(t, finNote(), docNote())
	svc = svc.WithEmbedder(emb, "bge-m3")
	rep, err := svc.BuildIndex()
	if err != nil || rep.Embedded != 1 || rep.Failed != 1 || len(st.vectors.Entries) != 1 {
		t.Fatalf("rep=%+v err=%v entries=%d", rep, err, len(st.vectors.Entries))
	}
	emb.down = errors.New("down")
	st.vectors = memory.VectorIndex{}
	st.hasVectors = false
	if _, err := svc.BuildIndex(); err == nil {
		t.Error("ни одного вектора — это ошибка, а не пустой индекс")
	}
	if _, err := memory.New(st, memory.Limits{}).BuildIndex(); !errors.Is(err, search.ErrNoSemanticLayer) {
		t.Errorf("без эмбеддера err = %v, want ErrNoSemanticLayer", err)
	}
}

func TestCheck_reportsStaleVectors(t *testing.T) {
	emb := &fakeEmbedder{}
	st, svc := newFind(t, finNote())
	st.snap = memory.Snapshot{
		Hot:   domain.TopicsStart + "\n- финансы\n" + domain.TopicsEnd,
		Notes: []domain.ColdNote{finNote().Note},
		Index: []domain.IndexEntry{{File: "budget-rules.md", Hook: "бюджет"}},
	}
	svc = svc.WithEmbedder(emb, "bge-m3")
	_, _ = svc.BuildIndex()
	if ps, _ := svc.Check(); len(ps) != 0 {
		t.Fatalf("свежий индекс: %v", ps)
	}
	st.snap.Notes[0].Digest = "d-fin-changed"
	ps, _ := svc.Check()
	if len(ps) != 1 || ps[0].Kind != domain.ProblemStaleVectors {
		t.Errorf("ps = %v", ps)
	}
}
