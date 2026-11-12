package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/docxwrite"
	"github.com/daniil/kb-engine/internal/adapter/versionbook"
	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// Новая итерация из чата (#548). Чат только предлагает: пока человек не
// подтвердил, на диск ничего не пишется, и только в память окна. Подтвержденное
// ложится НОВЫМ docx рядом с семейством, существующие файлы не меняются и не
// перезаписываются никогда.

// maxProposalBytes — предел текста новой итерации из чата.
const maxProposalBytes = 1 << 20

// maxPending — сколько неотвеченных предложений окно держит в памяти.
const maxPending = 20

// pendingIteration is a new iteration that the chat offered and the person has not answered yet.
type pendingIteration struct {
	ID, ChatID, Family, Folder, File, Text, Description, Note string
}

// iterationView is a proposal as the page shows it.
type iterationView struct {
	ID          string `json:"id"`
	ChatID      string `json:"chatId"`
	Family      string `json:"family"`
	Folder      string `json:"folder"`
	File        string `json:"file"`
	Description string `json:"description"`
	Note        string `json:"note"`
	Size        int    `json:"size"`
}

func (p pendingIteration) view() iterationView {
	return iterationView{p.ID, p.ChatID, p.Family, p.Folder, p.File, p.Description, p.Note, len(p.Text)}
}

// proposeVersionFor returns the propose_version tool of the chat; nil, когда у окна нет вкладки «Артефакты».
func (d *chatDesk) proposeVersionFor(chatID string) func(ctx context.Context, p chat.VersionProposal) error {
	if d.versions == nil {
		return nil
	}
	return func(ctx context.Context, p chat.VersionProposal) error {
		if len(p.Text) > maxProposalBytes {
			return errors.New("текст слишком большой для одной версии")
		}
		fams, err := d.versions.Families(ctx, p.Family, false)
		switch {
		case err != nil:
			return err
		case len(fams) == 0:
			return errors.New("документ не найден: уточните имя")
		case len(fams) > 1:
			return errors.New("найдено несколько документов, уточните имя")
		}
		fam := fams[0]
		latest := fam.Versions[len(fam.Versions)-1]
		pi := pendingIteration{ID: newChatID(), ChatID: chatID, Family: fam.Name, Folder: filepath.Dir(latest.Path),
			File: strings.ReplaceAll(fam.Name, " ", "_") + "_" + d.now().Format("2006_01_02") + ".docx", Text: p.Text, Description: p.Description, Note: p.Note}
		d.pmu.Lock()
		if len(d.proposals) == maxPending {
			d.proposals = d.proposals[1:] // самое старое неотвеченное уступает место
		}
		d.proposals = append(d.proposals, pi)
		d.pmu.Unlock()
		if d.versionNew != nil {
			d.versionNew(pi.view())
		}
		return nil
	}
}

// versionProposals lists the proposals nobody answered yet.
func (d *chatDesk) versionProposals() []iterationView {
	d.pmu.Lock()
	defer d.pmu.Unlock()
	out := make([]iterationView, 0, len(d.proposals))
	for _, p := range d.proposals {
		out = append(out, p.view())
	}
	return out
}

// dropVersionProposal forgets a proposal: файл не создается.
func (d *chatDesk) dropVersionProposal(id string) {
	d.pmu.Lock()
	defer d.pmu.Unlock()
	for i, p := range d.proposals {
		if p.ID == id {
			d.proposals = append(d.proposals[:i:i], d.proposals[i+1:]...)
			return
		}
	}
}

// saveVersionProposal creates the proposal as a new docx and marks it with the
// note of the person. Ошибка до записи оставляет предложение в ожидании: человек
// исправит поля и повторит.
func (d *chatDesk) saveVersionProposal(ctx context.Context, id, description, note string) (string, error) {
	mark := joinMark(note, description)
	if n := len([]rune(mark)); n > versionbook.MaxNoteRunes {
		return "", fmt.Errorf("заметка длиннее %d знаков (%d): сократите описание", versionbook.MaxNoteRunes, n)
	}
	d.pmu.Lock()
	var pi pendingIteration
	found := false
	for _, p := range d.proposals {
		if p.ID == id {
			pi, found = p, true
		}
	}
	d.pmu.Unlock()
	if !found {
		return "", errors.New("такого предложения нет: возможно, оно уже сохранено или отклонено")
	}
	data, err := docxwrite.Bytes(pi.Text)
	if err != nil {
		return "", err
	}
	path, err := createNew(pi.Folder, pi.File, data)
	if err != nil {
		return "", err
	}
	d.dropVersionProposal(id)
	// Новый файл должен встать в ленту, чтобы заметка нашла свою версию.
	if _, err := d.versions.Families(ctx, "", true); err != nil {
		return path, fmt.Errorf("файл сохранен, но список версий не обновился: %w", err)
	}
	if mark != "" {
		if err := d.versions.Mark(path, mark); err != nil {
			return path, fmt.Errorf("файл сохранен, но заметка не поставлена: %w", err)
		}
	}
	return path, nil
}

// joinMark is the note of the new version: что нового, затем описание файла.
func joinMark(note, description string) string {
	var parts []string
	for _, s := range []string{strings.TrimSpace(note), strings.TrimSpace(description)} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

// createNew writes data to a file that did not exist: занятое имя получает
// « (2)», « (3)». O_EXCL — существующий файл не может быть открыт на запись.
func createNew(dir, name string, data []byte) (string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for n := 1; n <= 99; n++ {
		candidate := name
		if n > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", base, n, ext)
		}
		path := filepath.Join(dir, candidate)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		// ponytail: при сбое записи (диск полон) недописанный файл остается: окно
		// ничего не удаляет само. Потолок: один обрывок рядом с документами.
		// Путь: писать во временный файл и переименовывать.
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			return "", fmt.Errorf("файл %s записан не до конца, удалите его: %w", path, err)
		}
		if err := f.Close(); err != nil {
			return "", fmt.Errorf("файл %s записан не до конца, удалите его: %w", path, err)
		}
		return path, nil
	}
	return "", errors.New("не подобрать свободное имя файла")
}
