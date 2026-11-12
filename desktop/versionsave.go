package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

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

// previewRunes — сколько знаков текста карточка показывает человеку для одобрения.
const previewRunes = 20000

// maxPending — сколько неотвеченных предложений окно держит в памяти.
const maxPending = 20

// savedVersion is the result of confirming a proposal: файл создан; Warning —
// что после этого не удалось (список версий, заметка), без отмены сохранения.
type savedVersion struct {
	Path    string `json:"path"`
	Warning string `json:"warning,omitempty"`
}

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
	Preview     string `json:"preview"` // начало текста: человек видит, что будет записано
}

func (p pendingIteration) view() iterationView {
	preview := p.Text
	if r := []rune(preview); len(r) > previewRunes {
		preview = string(r[:previewRunes]) + "…"
	}
	return iterationView{p.ID, p.ChatID, p.Family, p.Folder, p.File, p.Description, p.Note, len(p.Text), preview}
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
		if n := len([]rune(joinMark(p.Note, p.Description))); n > versionbook.MaxNoteRunes {
			return fmt.Errorf("описание файла и «что нового» вместе длиннее %d знаков (%d): сократи их", versionbook.MaxNoteRunes, n)
		}
		fam, err := d.proposalFamily(ctx, p.Family)
		if err != nil {
			return err
		}
		if n := d.longestVersion(ctx, fam); n > chat.MaxReadRunes {
			return fmt.Errorf("документ длиннее, чем модель читает за раз (%d знаков из %d): новую версию из чата сохранить нельзя — ни по прочитанному, ни по тексту, вставленному в чат, ни в новом чате. Скажи человеку, что длинный документ правят в Word или Pages, и других обходов не предлагай", n, chat.MaxReadRunes)
		}
		latest := fam.Versions[len(fam.Versions)-1]
		pi := pendingIteration{ID: newChatID(), ChatID: chatID, Family: fam.Name, Folder: filepath.Dir(latest.Path),
			File: fileSafe(fam.Name) + "_" + d.now().Format("2006_01_02") + ".docx", Text: p.Text, Description: p.Description, Note: p.Note}
		d.pmu.Lock()
		if len(d.proposals) >= maxPending {
			d.pmu.Unlock()
			return errors.New("слишком много предложений ждут ответа: человек их еще не просмотрел, не предлагай новых")
		}
		d.proposals = append(d.proposals, pi)
		d.pmu.Unlock()
		if d.versionNew != nil {
			d.versionNew(pi.view())
		}
		return nil
	}
}

// proposalFamily finds the one document the proposal is about: точное имя
// важнее подстроки («КП» и «КП Ромашка» — разные документы).
func (d *chatDesk) proposalFamily(ctx context.Context, name string) (versionbook.FamilyView, error) {
	fams, err := d.versions.Families(ctx, name, false)
	if err != nil {
		return versionbook.FamilyView{}, err
	}
	var exact []versionbook.FamilyView
	for _, f := range fams {
		if strings.EqualFold(f.Name, strings.TrimSpace(name)) {
			exact = append(exact, f)
		}
	}
	switch {
	case len(exact) == 1:
		return exact[0], nil
	case len(fams) == 0:
		return versionbook.FamilyView{}, errors.New("документ не найден: уточните имя")
	case len(fams) > 1:
		return versionbook.FamilyView{}, errors.New("найдено несколько документов, уточните имя")
	}
	return fams[0], nil
}

// longestVersion is the length in runes of the longest readable version of fam:
// модель читает версию не длиннее chat.MaxReadRunes, и новая итерация по
// обрезанному тексту потеряла бы конец документа (живая проверка 12.11).
// Проверяются все версии: модель могла переписать и старую. Нечитаемая версия
// пропускается — ее модель и не видела.
func (d *chatDesk) longestVersion(ctx context.Context, fam versionbook.FamilyView) int {
	n := 0
	for _, v := range fam.Versions {
		if text, err := d.versions.Text(ctx, v.Path); err == nil {
			n = max(n, utf8.RuneCountInString(text))
		}
	}
	return n
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

// claim takes the proposal out of the queue: сохранить его может только один
// вызов, двойной щелчок не создает дубль.
func (d *chatDesk) claim(id string) (pendingIteration, bool) {
	d.pmu.Lock()
	defer d.pmu.Unlock()
	for i, p := range d.proposals {
		if p.ID == id {
			d.proposals = append(d.proposals[:i:i], d.proposals[i+1:]...)
			return p, true
		}
	}
	return pendingIteration{}, false
}

// restore puts a claimed proposal back: запись не удалась, человек повторит.
func (d *chatDesk) restore(p pendingIteration) {
	d.pmu.Lock()
	d.proposals = append(d.proposals, p)
	d.pmu.Unlock()
}

// saveVersionProposal creates the proposal as a new docx and marks it with the
// note of the person. Ошибка ДО записи возвращает предложение в ожидание: человек
// исправит поля и повторит. Что не удалось ПОСЛЕ записи (список версий, заметка),
// идет предупреждением: файл уже создан, и повтор дал бы дубль.
func (d *chatDesk) saveVersionProposal(ctx context.Context, id, description, note string) (savedVersion, error) {
	mark := joinMark(note, description)
	if n := len([]rune(mark)); n > versionbook.MaxNoteRunes {
		return savedVersion{}, fmt.Errorf("заметка длиннее %d знаков (%d): сократите описание", versionbook.MaxNoteRunes, n)
	}
	pi, ok := d.claim(id)
	if !ok {
		return savedVersion{}, errors.New("такого предложения нет: возможно, оно уже сохранено или отклонено")
	}
	data, err := docxwrite.Bytes(pi.Text)
	if err != nil {
		d.restore(pi)
		return savedVersion{}, err
	}
	path, err := createNew(pi.Folder, pi.File, data)
	if err != nil {
		d.restore(pi)
		return savedVersion{}, err
	}
	// Новый файл должен встать в ленту, чтобы заметка нашла свою версию.
	if _, err := d.versions.Families(ctx, "", true); err != nil {
		return savedVersion{Path: path, Warning: "список версий не обновился: " + err.Error()}, nil
	}
	if mark != "" {
		if err := d.versions.Mark(path, mark); err != nil {
			return savedVersion{Path: path, Warning: "заметка не поставлена: " + err.Error()}, nil
		}
	}
	return savedVersion{Path: path}, nil
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

// fileSafe makes a family name fit a file name: разделители пути, двоеточия и
// управляющие знаки уходят в «_», пробелы тоже; точки и «_» по краям срезаются,
// чтобы файл не вышел скрытым и не попал в подкаталог.
func fileSafe(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == ' ' || r < 0x20 || r == 0x7f:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	if out := strings.Trim(b.String(), "._"); out != "" {
		return out
	}
	return "версия"
}

// dropChatProposals forgets the proposals of a deleted chat: их текст иначе
// лежал бы в памяти до перезапуска.
func (d *chatDesk) dropChatProposals(chatID string) {
	d.pmu.Lock()
	defer d.pmu.Unlock()
	d.proposals = slices.DeleteFunc(d.proposals, func(p pendingIteration) bool { return p.ChatID == chatID })
}
