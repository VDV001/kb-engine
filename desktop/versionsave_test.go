package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	path, err := r.cd.saveVersionProposal(context.Background(), p.ID, "КП для Ромашки, правка", "добавлена гарантия, срок 5 дней")
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
	p1, err := r.cd.saveVersionProposal(context.Background(), first.ID, "d", "n")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := r.cd.saveVersionProposal(context.Background(), second.ID, "d", "n")
	if err != nil {
		t.Fatal(err)
	}
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
