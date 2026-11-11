package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

const draftChatID = "0123456789abcdef"

func askedChat(t *testing.T, d *chatDesk) string {
	t.Helper()
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	c, err := d.Ask(askReq{Question: "вопрос"})
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// Черновик переживает перезапуск: новое хранилище над той же папкой отдает
// его, а файл лежит рядом с историей и открыт только владельцу.
func TestChatDraft_SurvivesRestart(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	if err := d.SaveDraft(draftChatID, "недописанный вопрос"); err != nil {
		t.Fatal(err)
	}
	again := newChatDesk(d.settings, d.dir)
	got, err := again.Draft(draftChatID)
	if err != nil || got != "недописанный вопрос" {
		t.Fatalf("Draft = %q, %v", got, err)
	}
	files, _ := filepath.Glob(filepath.Join(d.dir, "*"+draftChatID+"*"))
	if len(files) != 1 {
		t.Fatalf("файлов черновика %d: %v", len(files), files)
	}
	if st, err := os.Stat(files[0]); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("права черновика: %v, %v", st, err)
	}
}

// Чат, которого еще нет (новый), тоже хранит черновик; чужие черновики не смешиваются.
func TestChatDraft_NewChatAndOthersAreSeparate(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	if err := d.SaveDraft("", "для нового"); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveDraft(draftChatID, "для чата"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Draft(""); got != "для нового" {
		t.Errorf("новый чат: %q", got)
	}
	if got, _ := d.Draft(draftChatID); got != "для чата" {
		t.Errorf("чат: %q", got)
	}
	if got, err := d.Draft("fedcba9876543210"); err != nil || got != "" {
		t.Errorf("нет черновика: %q, %v", got, err)
	}
}

// Пустой текст снимает черновик, файл не остается.
func TestChatDraft_EmptyClears(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	_ = d.SaveDraft(draftChatID, "текст")
	if err := d.SaveDraft(draftChatID, "  \n"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Draft(draftChatID); got != "" {
		t.Fatalf("черновик жив: %q", got)
	}
	if files, _ := filepath.Glob(filepath.Join(d.dir, "*"+draftChatID+"*")); len(files) != 0 {
		t.Fatalf("файл остался: %v", files)
	}
}

// Отправка вопроса снимает черновик и чата, и «нового» (из него чат родился);
// удаление чата уносит его черновик.
func TestChatDraft_AskAndDeleteClearIt(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	_ = d.SaveDraft("", "первый вопрос")
	id := askedChat(t, d)
	if got, _ := d.Draft(""); got != "" {
		t.Errorf("черновик нового чата пережил отправку: %q", got)
	}
	_ = d.SaveDraft(id, "второй")
	if _, err := d.Ask(askReq{ID: id, Question: "второй"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Draft(id); got != "" {
		t.Errorf("черновик пережил отправку: %q", got)
	}
	_ = d.SaveDraft(id, "третий")
	if err := d.Delete(id); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Draft(id); got != "" {
		t.Errorf("черновик пережил удаление чата: %q", got)
	}
}

// Граница доверия: id приходит со страницы, размер ограничен.
func TestChatDraft_Refusals(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	for _, id := range []string{"../etc/passwd", "short", "0123456789ABCDEF", "0123456789abcdef/x"} {
		if err := d.SaveDraft(id, "x"); err == nil {
			t.Errorf("SaveDraft(%q) принят", id)
		}
		if _, err := d.Draft(id); err == nil {
			t.Errorf("Draft(%q) принят", id)
		}
	}
	if err := d.SaveDraft(draftChatID, strings.Repeat("я", maxDraft)); err == nil {
		t.Error("черновик больше предела принят")
	}
}
