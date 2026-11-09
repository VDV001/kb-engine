package memorydir_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/memorydir"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

// Скелет обязан проходить собственную проверку: иначе первая же `check`
// после `init` ругнется на то, что мы сами положили.
func TestInit_skeletonPassesItsOwnCheck(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem")
	st := memorydir.New(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"HOT.md", "cold/INDEX.md", "journal"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("нет %s: %v", f, err)
		}
	}
	ps, err := memory.New(st, memory.Limits{}).Check()
	if err != nil || len(ps) != 0 {
		t.Errorf("скелет не проходит проверку: %v, %v", ps, err)
	}
}

func TestInit_refusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	hot := filepath.Join(dir, "HOT.md")
	if err := os.WriteFile(hot, []byte("мои правки"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Init(); !errors.Is(err, domain.ErrMemoryExists) {
		t.Fatalf("err = %v, want ErrMemoryExists", err)
	}
	if b, _ := os.ReadFile(hot); string(b) != "мои правки" {
		t.Errorf("правки человека затерты: %q", b)
	}
}

// withTopicList кладет в горячий слой перечень тем, как это делает `memory topics`.
func withTopicList(t *testing.T, dir string) {
	t.Helper()
	hot := "# Горячая\n\n" + domain.TopicsStart + "\n- стиль\n" + domain.TopicsEnd + "\n"
	if err := os.WriteFile(filepath.Join(dir, "HOT.md"), []byte(hot), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := memorydir.New(dir).Seal(); err != nil { // правка руками подтверждена печатью
		t.Fatal(err)
	}
}

func TestAdd_writesNoteAndIndexLine(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	n, _ := domain.NewColdNote("owner-likes-short", "стиль", "владелец любит короткие ответы")
	if err := st.Add(n, "Тело записи."); err != nil {
		t.Fatal(err)
	}
	snap, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Notes) != 1 || snap.Notes[0].Description != "владелец любит короткие ответы" || snap.Notes[0].Name != "owner-likes-short" || snap.Notes[0].Topic != "стиль" {
		t.Errorf("notes = %+v", snap.Notes)
	}
	if len(snap.Index) != 1 || snap.Index[0].File != "owner-likes-short.md" || snap.Index[0].Hook != "владелец любит короткие ответы" {
		t.Errorf("index = %+v", snap.Index)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "cold", "owner-likes-short.md"))
	if !strings.Contains(string(body), "Тело записи.") {
		t.Errorf("тела нет в файле: %q", body)
	}
	withTopicList(t, dir)
	if ps, _ := memory.New(st, memory.Limits{}).Check(); len(ps) != 0 {
		t.Errorf("после add проверка красная: %v", ps)
	}
}

func TestAdd_refusesDuplicate(t *testing.T) {
	st := memorydir.New(t.TempDir())
	_ = st.Init()
	n, _ := domain.NewColdNote("dup", "стиль", "первая")
	if err := st.Add(n, "a"); err != nil {
		t.Fatal(err)
	}
	if err := st.Add(n, "b"); !errors.Is(err, domain.ErrNoteExists) {
		t.Fatalf("err = %v, want ErrNoteExists", err)
	}
}

// Load должен видеть записи, которые человек или агент положил мимо `add`:
// именно их и ловит проверка.
func TestLoad_seesHandWrittenNoteMissingFromIndex(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	hand := "---\nname: handmade\ntopic: стиль\ndescription: положил руками\n---\n\nтекст\n"
	if err := os.WriteFile(filepath.Join(dir, "cold", "handmade.md"), []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	withTopicList(t, dir)
	ps, err := memory.New(st, memory.Limits{}).Check()
	if err != nil || len(ps) != 1 || ps[0].Kind != domain.ProblemNotIndexed {
		t.Fatalf("ps=%v err=%v", ps, err)
	}
}

func TestDefaultPath(t *testing.T) {
	got := memorydir.DefaultPath("/Users/x")
	want := "/Users/x/Library/Application Support/kb-desktop/dvk-memory"
	if got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
}

// Журнал в обычный поиск и в холодные записи не попадает: файлы в journal/
// не читаются как записи и не требуют строк индекса.
func TestLoad_ignoresJournal(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	if err := os.WriteFile(filepath.Join(dir, "journal", "2026-11-09.md"), []byte("хроника без шапки"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := st.Load()
	if err != nil || len(snap.Notes) != 0 {
		t.Fatalf("notes=%v err=%v", snap.Notes, err)
	}
}

func TestLoad_reportsIndexSize(t *testing.T) {
	st := memorydir.New(t.TempDir())
	_ = st.Init()
	n, _ := domain.NewColdNote("a", "стиль", "про a")
	_ = st.Add(n, "x")
	snap, _ := st.Load()
	if snap.IndexRunes == 0 {
		t.Error("IndexRunes не посчитан")
	}
}

func TestSaveHot_replacesAtomically(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	if err := st.SaveHot("новый горячий\n"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "HOT.md"))
	if string(b) != "новый горячий\n" {
		t.Errorf("HOT.md = %q", b)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".hot-*"))
	if len(left) != 0 {
		t.Errorf("остались временные файлы: %v", left)
	}
	info, _ := os.Stat(filepath.Join(dir, "HOT.md"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("права HOT.md = %v, want 0600", info.Mode().Perm())
	}
}

func agentNote(t *testing.T, name string) domain.ColdNote {
	t.Helper()
	n, err := domain.NewColdNote(name, "стиль", "предложил агент")
	if err != nil {
		t.Fatal(err)
	}
	n.Origin = domain.OriginAgent
	return n
}

// Очередь не видна ни проверке, ни перечню: pending/ не часть холодного слоя.
func TestPending_staysOutOfColdLayer(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	if err := st.AddPending(agentNote(t, "p1"), "тело"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddPending(agentNote(t, "p1"), "еще"); !errors.Is(err, domain.ErrNoteExists) {
		t.Errorf("дубль в очереди: %v", err)
	}
	snap, _ := st.Load()
	if len(snap.Notes) != 0 || len(snap.Index) != 0 {
		t.Fatalf("очередь попала в холодный слой: %+v", snap)
	}
	list, err := st.Pending()
	if err != nil || len(list) != 1 || list[0].Note.Name != "p1" || list[0].Note.Origin != domain.OriginAgent || list[0].Body != "тело" {
		t.Fatalf("Pending = %+v, %v", list, err)
	}
	if _, err := st.ReadPending("нет"); !errors.Is(err, domain.ErrNoSuchPending) {
		t.Errorf("ReadPending чужого: %v", err)
	}
}

func TestPromotePending_movesToColdWithIndexLine(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	_ = st.AddPending(agentNote(t, "p1"), "тело")
	if err := st.PromotePending("p1"); err != nil {
		t.Fatal(err)
	}
	snap, _ := st.Load()
	if len(snap.Notes) != 1 || snap.Notes[0].Origin != domain.OriginAgent || len(snap.Index) != 1 {
		t.Fatalf("snap = %+v", snap)
	}
	if left, _ := st.Pending(); len(left) != 0 {
		t.Errorf("в очереди осталось: %v", left)
	}
	// Холодная запись с тем же именем уже есть: очередь ее не затирает.
	// AddPending такое имя уже не принимает (занято в cold/), поэтому файл кладем руками.
	dup := "---\nname: p1\ntopic: стиль\norigin: agent\ndescription: дубль\n---\n\nвторой\n"
	if err := os.WriteFile(filepath.Join(dir, "pending", "p1.md"), []byte(dup), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.AddPending(agentNote(t, "p1"), "x"); !errors.Is(err, domain.ErrNoteExists) {
		t.Errorf("AddPending с именем из cold/: %v", err)
	}
	if err := st.PromotePending("p1"); !errors.Is(err, domain.ErrNoteExists) {
		t.Errorf("затирание холодной записи: %v", err)
	}
	if left, _ := st.Pending(); len(left) != 1 {
		t.Error("отказ не должен терять запись из очереди")
	}
}

func TestArchivePending_movesToJournal(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	_ = st.AddPending(agentNote(t, "p1"), "тело")
	if err := st.ArchivePending("p1", "2026-11-09-rejected-p1.md"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "journal", "2026-11-09-rejected-p1.md"))
	if err != nil || !strings.Contains(string(b), "тело") {
		t.Fatalf("журнал: %q, %v", b, err)
	}
	if left, _ := st.Pending(); len(left) != 0 {
		t.Errorf("в очереди осталось: %v", left)
	}
}

func TestColdTexts_coldOnlyWithDigest(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	n, _ := domain.NewColdNote("a", "стиль", "про a")
	_ = st.Add(n, "тело a")
	_ = st.AddPending(agentNote(t, "p1"), "ждет")
	_ = os.WriteFile(filepath.Join(dir, "journal", "2026-11-09.md"), []byte("хроника"), 0o600)

	texts, err := st.ColdTexts()
	if err != nil || len(texts) != 1 || texts[0].Note.Name != "a" || texts[0].Body != "тело a" {
		t.Fatalf("texts = %+v, %v (журнал и очередь в поиск не входят)", texts, err)
	}
	d1 := texts[0].Note.Digest
	if d1 == "" {
		t.Fatal("Digest пуст")
	}
	snap, _ := st.Load()
	if snap.Notes[0].Digest != d1 {
		t.Errorf("Load и ColdTexts считают отпечаток по-разному: %q vs %q", snap.Notes[0].Digest, d1)
	}
	// правка файла руками меняет отпечаток
	b, _ := os.ReadFile(filepath.Join(dir, "cold", "a.md"))
	_ = os.WriteFile(filepath.Join(dir, "cold", "a.md"), append(b, []byte("правка\n")...), 0o600)
	texts, _ = st.ColdTexts()
	if texts[0].Note.Digest == d1 {
		t.Error("отпечаток не изменился после правки")
	}
}

func TestVectors_roundTripAndAbsent(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	if _, err := st.LoadVectors(); !errors.Is(err, memory.ErrNoVectors) {
		t.Fatalf("нет файла: err = %v, want ErrNoVectors", err)
	}
	in := memory.VectorIndex{Model: "bge-m3", Dims: 2, Built: "2026-11-09T12:00:00Z",
		Entries: map[string]memory.VectorEntry{"a": {Digest: "d", Vector: []float32{0.5, -1}}}}
	if err := st.SaveVectors(in); err != nil {
		t.Fatal(err)
	}
	out, err := st.LoadVectors()
	if err != nil || out.Model != "bge-m3" || out.Dims != 2 || out.Entries["a"].Digest != "d" || out.Entries["a"].Vector[1] != -1 {
		t.Fatalf("out = %+v, %v", out, err)
	}
	info, _ := os.Stat(filepath.Join(dir, "cold", ".vectors.json"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("права = %v, want 0600", info.Mode().Perm())
	}
	// файл векторов не должен читаться как запись холодного слоя
	if snap, _ := st.Load(); len(snap.Notes) != 0 {
		t.Errorf("векторы попали в записи: %v", snap.Notes)
	}
	// битый файл — ошибка, а не «векторов нет»
	_ = os.WriteFile(filepath.Join(dir, "cold", ".vectors.json"), []byte("{"), 0o600)
	if _, err := st.LoadVectors(); err == nil || errors.Is(err, memory.ErrNoVectors) {
		t.Errorf("битый файл: err = %v", err)
	}
}

func TestInit_skeletonHasPortraitAndTopicMarkers(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	b, _ := os.ReadFile(filepath.Join(dir, "HOT.md"))
	for _, m := range []string{domain.PortraitStart, domain.PortraitEnd, domain.TopicsStart, domain.TopicsEnd} {
		if !strings.Contains(string(b), m) {
			t.Errorf("в скелете нет метки %s", m)
		}
	}
}

func TestLimits_roundTrip(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	if got, err := st.Limits(); err != nil || got != (memory.Limits{}) {
		t.Fatalf("без файла: %+v, %v (ноль = умолчания)", got, err)
	}
	if err := st.SaveLimits(memory.Limits{Hot: 6000, Index: 40000}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Limits()
	if err != nil || got.Hot != 6000 || got.Index != 40000 {
		t.Fatalf("got %+v, %v", got, err)
	}
	info, _ := os.Stat(filepath.Join(dir, "memory.json"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("права = %v, want 0600", info.Mode().Perm())
	}
	if err := st.SaveLimits(memory.Limits{Hot: 5}); !errors.Is(err, domain.ErrInvalidLimits) {
		t.Errorf("негодный потолок: %v", err)
	}
	if got, _ := st.Limits(); got.Hot != 6000 {
		t.Error("отказ не должен затирать прежний потолок")
	}
	_ = os.WriteFile(filepath.Join(dir, "memory.json"), []byte("{"), 0o600)
	if _, err := st.Limits(); err == nil {
		t.Error("битый memory.json должен быть ошибкой, а не умолчанием")
	}
}

func TestJournalNames(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	for _, n := range []string{"2026-11-09-b.md", "2026-11-08-a.md", "note.txt"} {
		_ = os.WriteFile(filepath.Join(dir, "journal", n), []byte("x"), 0o600)
	}
	got, err := st.JournalNames()
	if err != nil || len(got) != 2 || got[0] != "2026-11-08-a.md" || got[1] != "2026-11-09-b.md" {
		t.Fatalf("got %v, %v (только .md, по имени)", got, err)
	}
}

// Тот же день, то же имя: повторное предложение и его отклонение не упираются в
// занятое имя архива и не застревают в очереди.
func TestArchivePending_uniqueNameInJournal(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	for range 2 {
		if err := st.AddPending(agentNote(t, "p1"), "тело"); err != nil {
			t.Fatal(err)
		}
		if err := st.ArchivePending("p1", "2026-11-09-rejected-p1.md"); err != nil {
			t.Fatalf("повторное архивирование: %v", err)
		}
	}
	names, _ := st.JournalNames()
	if len(names) != 2 {
		t.Errorf("в журнале должно быть 2 файла, есть %v", names)
	}
}

func TestPromotePending_rollsBackWhenTheIndexCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root читает любой файл")
	}
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	_ = st.AddPending(agentNote(t, "p1"), "тело")
	idx := filepath.Join(dir, "cold", "INDEX.md")
	if err := os.Chmod(idx, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(idx, 0o600) })
	if err := st.PromotePending("p1"); err == nil {
		t.Fatal("индекс не пишется, а PromotePending сказал «готово»")
	}
	if _, err := os.Stat(filepath.Join(dir, "cold", "p1.md")); err == nil {
		t.Error("запись осталась в cold/ без строки индекса")
	}
	if left, _ := st.Pending(); len(left) != 1 {
		t.Errorf("запись должна вернуться в очередь, там %d", len(left))
	}
}

func TestSeal_followsSystemWritesAndSeesOutsideEdits(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	sealed, err := st.SealedDigest()
	if err != nil || sealed == "" {
		t.Fatalf("Init должен запечатать скелет: %q, %v", sealed, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "HOT.md"))
	if sealed != domain.HotDigest(string(b)) {
		t.Error("печать скелета не совпала с файлом")
	}
	if err := st.SaveHot("новый горячий\n"); err != nil {
		t.Fatal(err)
	}
	if sealed, _ = st.SealedDigest(); sealed != domain.HotDigest("новый горячий\n") {
		t.Error("SaveHot должен перепечатывать")
	}
	_ = os.WriteFile(filepath.Join(dir, "HOT.md"), []byte("подсунули"), 0o600)
	if sealed, _ = st.SealedDigest(); sealed == domain.HotDigest("подсунули") {
		t.Error("правка мимо системы не должна менять печать")
	}
	if err := st.Seal(); err != nil {
		t.Fatal(err)
	}
	if sealed, _ = st.SealedDigest(); sealed != domain.HotDigest("подсунули") {
		t.Error("Seal не подтвердил текущий файл")
	}
	info, _ := os.Stat(filepath.Join(dir, ".seal"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("права .seal = %v", info.Mode().Perm())
	}
}

func TestSealedDigest_absentMeansNoSeal(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	_ = os.Remove(filepath.Join(dir, ".seal"))
	if d, err := st.SealedDigest(); err != nil || d != "" {
		t.Errorf("d=%q err=%v: память без печати — не нарушение", d, err)
	}
}

func TestDecisions_appendOnlyOneLineEach(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	for _, l := range []string{"первое", "второе\nс переводом строки", "третье"} {
		if err := st.AppendDecision(l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Decisions(2)
	if err != nil || len(got) != 2 || got[1] != "третье" || strings.Contains(got[0], "\n") {
		t.Fatalf("got %q, %v", got, err)
	}
	if all, _ := st.Decisions(100); len(all) != 3 {
		t.Errorf("всего %d записей, want 3", len(all))
	}
	if none, err := memorydir.New(t.TempDir()).Decisions(5); err != nil || len(none) != 0 {
		t.Errorf("нет файла — пустой журнал: %v, %v", none, err)
	}
}

// Здоровье меряет, сколько предложение ждёт: срок берется со времени файла в очереди.
func TestPending_reportsSinceFromTheFile(t *testing.T) {
	dir := t.TempDir()
	st := memorydir.New(dir)
	_ = st.Init()
	if err := st.AddPending(agentNote(t, "p1"), "тело"); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	if err := os.Chtimes(filepath.Join(dir, "pending", "p1.md"), old, old); err != nil {
		t.Fatal(err)
	}
	list, err := st.Pending()
	if err != nil || len(list) != 1 || !list[0].Since.Equal(old) {
		t.Fatalf("Pending = %+v, %v", list, err)
	}
}
