package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/versionbook"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/usage"
)

// chatVersions gives the chat the families of the «Артефакты» tab: те же
// списки, подписи «что нового», заметки и значки; chat и вкладка читают одно
// и то же, каждый не считает свое (#547).
type chatVersions struct{ d *versionsDesk }

// versionsAccess returns the versions of documents for the model, nil when the window has none.
func (d *chatDesk) versionsAccess() chat.Versions {
	if d.versions == nil {
		return nil
	}
	return chatVersions{d.versions}
}

// Families returns the families matching q with the notes and badges of the person.
func (c chatVersions) Families(ctx context.Context, q string) ([]chat.VersionFamily, error) {
	fams, err := c.d.Families(ctx, q, false)
	if err != nil {
		return nil, err
	}
	out := make([]chat.VersionFamily, 0, len(fams))
	for _, f := range fams {
		cf := chat.VersionFamily{Name: f.Name}
		for _, v := range f.Versions {
			cf.Versions = append(cf.Versions, chat.VersionInfo{Path: v.Path, Date: v.Date, Size: v.Size, Note: v.Note, Mark: v.Mark, Current: v.Current, Dups: v.Dups})
		}
		out = append(out, cf)
	}
	return out, nil
}

// Text returns the text of a version of the last list.
func (c chatVersions) Text(ctx context.Context, path string) (string, error) {
	return c.d.Text(ctx, path)
}

// describeLines и describeRunes — сколько строк каждой версии и какой длины уходит
// модели: описать изменение можно по немногому, а провайдеру лишнее не нужно.
const (
	describeLines  = 40
	describeRunes  = 200
	describeSystem = `Ты ведешь журнал версий документа. По строкам, которых нет в другой версии, опиши одним-двумя предложениями по-русски, что изменилось в новой версии относительно старой. Только по этим строкам, ничего не добавляй от себя, без вступлений и без пересказа документа.`
)

// DescribeVersion asks the model what changed in the newer version (шаг 3, #547).
// consent — человек увидел, что строки версий уйдут провайдеру модели: без него
// не уходит ничего. Модели отдаются только строки, которых нет в другой версии,
// а не файлы; результат человек правит и сохраняет заметкой сам.
func (d *chatDesk) DescribeVersion(older, newer string, consent bool) (string, error) {
	if !consent {
		return "", errors.New("нужно согласие: строки версий уйдут провайдеру модели")
	}
	if d.versions == nil {
		return "", errNoVersions
	}
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	cmp, err := d.versions.Compare(ctx, older, newer)
	if err != nil {
		return "", err
	}
	removed, added := changedLines(cmp.Old), changedLines(cmp.New)
	if len(removed) == 0 && len(added) == 0 {
		return "", errors.New("строки версий не отличаются: описывать нечего (правка оформления или порядка)")
	}
	c := d.config()
	key, err := d.modelKey(c)
	if err != nil {
		return "", err
	}
	model := d.metered(d.model(c, key), c, usage.Fixed(domain.CallDescribe), "")
	reply, err := model.Complete(ctx, []chat.ModelMessage{
		{Role: chat.RoleSystem, Content: describeSystem},
		{Role: chat.RoleUser, Content: describePrompt(filepath.Base(newer), removed, added)},
	}, nil)
	if err != nil {
		return "", errors.New(chatError(err))
	}
	text := strings.TrimSpace(reply.Content)
	if text == "" {
		return "", errors.New("модель не ответила: попробуйте еще раз")
	}
	if r := []rune(text); len(r) > versionbook.MaxNoteRunes {
		text = string(r[:versionbook.MaxNoteRunes-1]) + "…"
	}
	return text, nil
}

// changedLines are the lines of a side that the other version lacks, clipped.
func changedLines(ls []compareLine) []string {
	var out []string
	for _, l := range ls {
		if !l.Changed {
			continue
		}
		if len(out) == describeLines {
			break
		}
		t := l.Text
		if r := []rune(t); len(r) > describeRunes {
			t = string(r[:describeRunes]) + "…"
		}
		out = append(out, t)
	}
	return out
}

func describePrompt(name string, removed, added []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Документ: %s\n\nУбрано (было в старой версии):\n", name)
	for _, l := range removed {
		b.WriteString("- " + l + "\n")
	}
	b.WriteString("\nДобавлено (есть в новой версии):\n")
	for _, l := range added {
		b.WriteString("- " + l + "\n")
	}
	return b.String()
}
