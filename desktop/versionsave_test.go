package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/versionbook"
	"github.com/daniil/kb-engine/internal/usecase/chat"
)

type saveRig struct {
	cd      *chatDesk
	vd      *versionsDesk
	root    string
	v1, v2  string
	events  []iterationView
	propose func(context.Context, chat.VersionProposal) error
}

func newSaveRig(t *testing.T) *saveRig {
	t.Helper()
	cd, _ := localChat(t)
	root, vd, v1, v2 := kp(t)
	r := &saveRig{cd: cd, vd: vd, root: root, v1: v1, v2: v2}
	cd.versions = vd
	cd.versionNew = func(p iterationView) { r.events = append(r.events, p) }
	r.propose = cd.proposeVersionFor("chat-1")
	return r
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}

func offer(t *testing.T, r *saveRig, text string) iterationView {
	t.Helper()
	before := len(r.events)
	if err := r.propose(context.Background(), chat.VersionProposal{Family: "кп", Text: text, Description: "КП для Ромашки", Note: "добавлена гарантия"}); err != nil {
		t.Fatal(err)
	}
	if len(r.events) != before+1 {
		t.Fatalf("странице не сказали о предложении: %d событий", len(r.events))
	}
	return r.events[before]
}

// Предложение ничего не создает: файл появится только после подтверждения.
func TestProposeVersion_WritesNothingUntilTheAnswer(t *testing.T) {
	r := newSaveRig(t)
	before := listDir(t, filepath.Join(r.root, "p"))
	p := offer(t, r, "Новый\nтекст")
	if got := listDir(t, filepath.Join(r.root, "p")); !slices.Equal(got, before) {
		t.Errorf("до подтверждения папка изменилась: %v -> %v", before, got)
	}
	if p.Family != "КП" || p.File != "КП_2026_11_06.docx" || p.Folder != filepath.Join(r.root, "p") || p.ID == "" || p.Description != "КП для Ромашки" || p.Note != "добавлена гарантия" {
		t.Errorf("предложение: %+v", p)
	}
	if list := r.cd.versionProposals(); len(list) != 1 || list[0].ID != p.ID {
		t.Errorf("ждущие предложения: %+v", list)
	}
}

func TestProposeVersion_NeedsExactlyOneDocument(t *testing.T) {
	r := newSaveRig(t)
	docxFile(t, filepath.Join(r.root, "p", "Смета_2026_11_01.docx"), "а")
	docxFile(t, filepath.Join(r.root, "p", "Смета_2026_11_09.docx"), "а", "б")
	if _, err := r.vd.Families(context.Background(), "", true); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{"нет такого": "договор", "несколько": ""} {
		if err := r.propose(context.Background(), chat.VersionProposal{Family: q, Text: "т", Description: "d", Note: "n"}); err == nil {
			t.Errorf("%s: предложение принято", name)
		}
	}
	if len(r.events) != 0 || len(r.cd.versionProposals()) != 0 {
		t.Errorf("отклоненное предложение дошло до человека: %+v", r.events)
	}
}

// Подтверждение создает новый docx рядом с семьей, ставит заметку и не трогает
// прежние версии ни байтом.
func TestSaveVersion_CreatesANewDocxAndLeavesTheOldOnesAlone(t *testing.T) {
	r := newSaveRig(t)
	b1, _ := os.ReadFile(r.v1)
	b2, _ := os.ReadFile(r.v2)
	p := offer(t, r, "Новый\nтекст")
	saved, err := r.cd.saveVersionProposal(context.Background(), p.ID, "КП для Ромашки, правка", "добавлена гарантия, срок 5 дней")
	path := saved.Path
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(r.root, "p", "КП_2026_11_06.docx") {
		t.Errorf("путь новой версии %q", path)
	}
	if text, err := r.vd.Text(context.Background(), path); err != nil || text != "Новый\nтекст\n" {
		t.Errorf("текст новой версии: %q %v", text, err)
	}
	a1, _ := os.ReadFile(r.v1)
	a2, _ := os.ReadFile(r.v2)
	if !bytes.Equal(a1, b1) || !bytes.Equal(a2, b2) {
		t.Error("прежние версии изменились")
	}
	fams, _ := r.vd.Families(context.Background(), "кп", false)
	var found *versionbook.VersionView
	for _, f := range fams {
		for i, v := range f.Versions {
			if v.Path == path {
				found = &f.Versions[i]
			}
		}
	}
	if found == nil || found.Mark != "добавлена гарантия, срок 5 дней · КП для Ромашки, правка" {
		t.Errorf("новая версия или ее заметка (правка человека) не встали в ленту: %+v", found)
	}
	if len(r.cd.versionProposals()) != 0 {
		t.Error("подтвержденное предложение осталось в ожидании")
	}
}

func TestSaveVersion_NeverOverwritesAnExistingFile(t *testing.T) {
	r := newSaveRig(t)
	first := offer(t, r, "Первый")
	second := offer(t, r, "Второй")
	s1, err := r.cd.saveVersionProposal(context.Background(), first.ID, "d", "n")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := r.cd.saveVersionProposal(context.Background(), second.ID, "d", "n")
	if err != nil {
		t.Fatal(err)
	}
	p1, p2 := s1.Path, s2.Path
	if p1 == p2 || !strings.HasSuffix(p2, " (2).docx") {
		t.Errorf("второй файл должен получить другое имя: %q и %q", p1, p2)
	}
	if text, _ := r.vd.Text(context.Background(), p1); text != "Первый\n" {
		t.Errorf("первый файл перезаписан: %q", text)
	}
}

func TestSaveVersion_UnknownOrDroppedProposalWritesNothing(t *testing.T) {
	r := newSaveRig(t)
	before := listDir(t, filepath.Join(r.root, "p"))
	if _, err := r.cd.saveVersionProposal(context.Background(), "нет-такого", "d", "n"); err == nil {
		t.Error("неизвестное предложение принято")
	}
	p := offer(t, r, "т")
	r.cd.dropVersionProposal(p.ID)
	if _, err := r.cd.saveVersionProposal(context.Background(), p.ID, "d", "n"); err == nil {
		t.Error("отклоненное предложение все равно сохранилось")
	}
	if got := listDir(t, filepath.Join(r.root, "p")); !slices.Equal(got, before) {
		t.Errorf("папка изменилась: %v -> %v", before, got)
	}
}

func TestSaveVersion_RefusesTooLongNoteAndHugeText(t *testing.T) {
	r := newSaveRig(t)
	p := offer(t, r, "т")
	if _, err := r.cd.saveVersionProposal(context.Background(), p.ID, strings.Repeat("я", 400), strings.Repeat("я", 400)); err == nil {
		t.Error("слишком длинная заметка принята")
	}
	if err := r.propose(context.Background(), chat.VersionProposal{Family: "кп", Text: strings.Repeat("я", maxProposalBytes+1), Description: "d", Note: "n"}); err == nil {
		t.Error("огромный текст принят")
	}
}

// Файл уже создан, а заметка не встала: это предупреждение, а не провал, иначе
// карточка залипает, и повтор дал бы дубль.
func TestSaveVersion_FailureAfterTheWriteIsAWarningNotAFailure(t *testing.T) {
	r := newSaveRig(t)
	p := offer(t, r, "Новый текст")
	dir := filepath.Dir(r.vd.MarksPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if f, err := os.CreateTemp(dir, "probe"); err == nil { // под root права не мешают
		_ = f.Close()
		_ = os.Remove(f.Name())
		t.Skip("каталог заметок пишется несмотря на chmod 0500")
	}
	saved, err := r.cd.saveVersionProposal(context.Background(), p.ID, "d", "n")
	if err != nil {
		t.Fatalf("после записи файла ошибка должна стать предупреждением: %v", err)
	}
	if _, statErr := os.Stat(saved.Path); statErr != nil || saved.Warning == "" {
		t.Errorf("файл %q и предупреждение %q", saved.Path, saved.Warning)
	}
	if len(r.cd.versionProposals()) != 0 {
		t.Error("предложение осталось в ожидании: повтор создал бы дубль")
	}
}

// Двойной щелчок и параллельные вызовы создают один файл.
func TestSaveVersion_TwoCallsAtOnceWriteOneFile(t *testing.T) {
	r := newSaveRig(t)
	p := offer(t, r, "Новый текст")
	before := len(listDir(t, filepath.Join(r.root, "p")))
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { _, errs[i] = r.cd.saveVersionProposal(context.Background(), p.ID, "d", "n") })
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 || len(listDir(t, filepath.Join(r.root, "p"))) != before+1 {
		t.Errorf("успешных сохранений %d, файлов добавилось %d: нужен ровно один", ok, len(listDir(t, filepath.Join(r.root, "p")))-before)
	}
}

// Человек одобряет текст, а не название: карточка показывает, что будет записано.
func TestProposeVersion_CardShowsTheTextToApprove(t *testing.T) {
	r := newSaveRig(t)
	p := offer(t, r, "Первая строка\nВторая строка")
	if !strings.Contains(p.Preview, "Вторая строка") || p.Size == 0 {
		t.Errorf("предпросмотр %q, размер %d", p.Preview, p.Size)
	}
	long := strings.Repeat("я", previewRunes+500)
	if got := offer(t, r, long); len([]rune(got.Preview)) > previewRunes+1 {
		t.Errorf("предпросмотр не ограничен: %d знаков", len([]rune(got.Preview)))
	}
}

func TestFileSafe_NoSeparatorsControlsOrLeadingDots(t *testing.T) {
	for in, want := range map[string]string{"КП Ромашка": "КП_Ромашка", "А/Б\\В:Г": "А_Б_В_Г", "..скрытый": "скрытый", "т\x00аб\n": "т_аб", "///": "версия"} {
		if got := fileSafe(in); got != want {
			t.Errorf("fileSafe(%q) = %q, ждали %q", in, got, want)
		}
	}
}

func TestProposeVersion_TooLongNoteIsRefusedWhenOffered(t *testing.T) {
	r := newSaveRig(t)
	err := r.propose(context.Background(), chat.VersionProposal{Family: "кп", Text: "т", Description: strings.Repeat("я", 300), Note: strings.Repeat("я", 300)})
	if err == nil || len(r.events) != 0 {
		t.Errorf("длинную заметку надо вернуть модели сразу, а не человеку: %v, событий %d", err, len(r.events))
	}
}

// Переполненная очередь не вытесняет молча: человек мог уже править карточку.
func TestProposeVersion_FullQueueRefusesInsteadOfEvicting(t *testing.T) {
	r := newSaveRig(t)
	for i := range maxPending {
		offer(t, r, fmt.Sprintf("текст %d", i))
	}
	if err := r.propose(context.Background(), chat.VersionProposal{Family: "кп", Text: "лишний", Description: "d", Note: "n"}); err == nil {
		t.Error("предложение сверх очереди принято")
	}
	if got := r.cd.versionProposals(); len(got) != maxPending || got[0].Size != len("текст 0") {
		t.Errorf("прежние предложения изменились: %d", len(got))
	}
}

func TestDeleteChat_DropsItsProposals(t *testing.T) {
	r := newSaveRig(t)
	offer(t, r, "т")
	other := r.cd.proposeVersionFor("chat-2")
	if err := other(context.Background(), chat.VersionProposal{Family: "кп", Text: "чужой", Description: "d", Note: "n"}); err != nil {
		t.Fatal(err)
	}
	r.cd.dropChatProposals("chat-1")
	left := r.cd.versionProposals()
	if len(left) != 1 || left[0].ChatID != "chat-2" {
		t.Errorf("после удаления чата осталось %+v", left)
	}
}

// Из двух подходящих имен берется то, что названо точно.
func TestProposeVersion_ExactNameBeatsASubstring(t *testing.T) {
	r := newSaveRig(t)
	docxFile(t, filepath.Join(r.root, "p", "КП Ромашка_2026_11_01.docx"), "а")
	docxFile(t, filepath.Join(r.root, "p", "КП Ромашка_2026_11_09.docx"), "а", "б")
	if _, err := r.vd.Families(context.Background(), "", true); err != nil {
		t.Fatal(err)
	}
	if err := r.propose(context.Background(), chat.VersionProposal{Family: "КП", Text: "т", Description: "d", Note: "n"}); err != nil {
		t.Fatalf("точное имя «КП» должно выбрать свой документ: %v", err)
	}
	if len(r.events) != 1 || r.events[0].Family != "КП" {
		t.Errorf("выбрано %+v", r.events)
	}
}

// Документ, у которого есть версия длиннее того, что модель читает за раз, новой
// версией из чата не предлагается: в docx ушел бы обрезанный текст (живая
// проверка 12.11). Документ выбирается так же, как при записи: точное имя
// главнее похожего (ревью #577: «Договор» рядом с «Договор Ромашка»), и длинной
// может быть не последняя версия.
func TestProposeVersion_RefusesDocumentLongerThanTheModelReads(t *testing.T) {
	long := strings.Repeat("я", chat.MaxReadRunes+1)
	for name, files := range map[string][][2]string{
		"последняя длинная": {{"Договор_2026_11_01.docx", "коротко"}, {"Договор_2026_11_09.docx", long}},
		"старая длинная":    {{"Договор_2026_11_01.docx", long}, {"Договор_2026_11_09.docx", "коротко"}},
	} {
		r := newSaveRig(t)
		for _, f := range files {
			docxFile(t, filepath.Join(r.root, "d", f[0]), f[1])
		}
		docxFile(t, filepath.Join(r.root, "d", "Договор Ромашка_2026_11_02.docx"), "другой документ")
		if _, err := r.vd.Families(context.Background(), "", true); err != nil {
			t.Fatal(err)
		}
		err := r.propose(context.Background(), chat.VersionProposal{Family: "Договор", Text: "новый текст", Description: "d", Note: "n"})
		if err == nil || !strings.Contains(err.Error(), "длиннее") {
			t.Errorf("%s: ошибка %v", name, err)
		}
		if len(r.events) != 0 {
			t.Errorf("%s: предложение дошло до человека", name)
		}
	}
}
