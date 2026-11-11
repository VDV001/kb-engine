package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Черновик чата (#540): набранный, но не отправленный вопрос лежит рядом с
// историей (<id>.draft, права 0600) и переживает перезапуск окна.

// maxDraft caps a draft in bytes: поле ввода не должно раздувать папку чатов.
const maxDraft = 64 << 10

// newChatDraft is the file name of the draft of a chat that does not exist yet.
const newChatDraft = "new"

// draftPath is the draft file of chat id ("" — еще не созданный чат); id
// приходит со страницы — только свой вид.
func (d *chatDesk) draftPath(id string) (string, error) {
	if id == "" {
		id = newChatDraft
	} else if !chatIDRe.MatchString(id) {
		return "", fmt.Errorf("нет такого чата: %q", id)
	}
	return filepath.Join(d.dir, id+".draft"), nil
}

// Draft returns the saved draft of chat id ("" — еще не созданный чат).
func (d *chatDesk) Draft(id string) (string, error) {
	p, err := d.draftPath(id)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(raw), err
}

// SaveDraft keeps text as the draft of chat id; пустой текст черновик снимает.
func (d *chatDesk) SaveDraft(id, text string) error {
	p, err := d.draftPath(id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return d.DropDraft(id)
	}
	if len(text) > maxDraft {
		return fmt.Errorf("черновик больше %d КБ не сохраняется", maxDraft>>10)
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d.dir, "draft.*.tmp") // 0600
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // после rename — уже нет
	if _, err := tmp.WriteString(text); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// DropDraft removes the draft of chat id; нет черновика — не ошибка.
func (d *chatDesk) DropDraft(id string) error {
	p, err := d.draftPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
