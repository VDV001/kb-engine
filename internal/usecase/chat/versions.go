package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/daniil/kb-engine/internal/usecase/versions"
)

// Versions — итерации документов для режима «модель ищет» (#547): те же
// семейства, подписи и отметки, что на вкладке «Артефакты».
type Versions interface {
	// Families — семейства по запросу, версии в каждом от старой к новой.
	Families(ctx context.Context, q string) ([]VersionFamily, error)
	// Text — текст версии по пути из Families.
	Text(ctx context.Context, path string) (string, error)
}

// VersionFamily — документ и его версии.
type VersionFamily struct {
	Name     string
	Versions []VersionInfo
}

// VersionInfo — одна версия: что нового и что отметил человек.
type VersionInfo struct {
	Path    string
	Date    string // 02.01.2006
	Size    int64
	Note    string // что нового относительно предыдущей
	Mark    string // заметка человека
	Current bool   // значок «актуальная»
	Dups    []string
}

// maxFamilies — сколько документов версий модель получает в одном ответе.
const maxFamilies = 5

// versionsPrompt is added when the window has the versions of documents.
const versionsPrompt = `
У документов бывают версии (итерации). Инструмент versions показывает версии с описанием «что нового» и пометками человека, open_version берет текст нужной версии: «последняя», «предыдущая», «первая», «v3», «от 05.11», «актуальная» или слова из заметки («отправленная Алексею»). Если подходит несколько версий или документов, спроси человека, а не угадывай.`

var versionTools = []Tool{
	{Name: "versions", Description: "Версии документа: даты, что нового в каждой, пометки человека и значок «актуальная». Ищет по имени документа.",
		Parameters: []byte(`{"type":"object","properties":{"q":{"type":"string","description":"имя документа или его часть"}},"required":["q"]}`)},
	{Name: "open_version", Description: "Прочитать текст одной версии документа. which — словами человека: последняя, предыдущая, первая, v3, от 05.11, актуальная или слова из его заметки.",
		Parameters: []byte(`{"type":"object","properties":{"q":{"type":"string","description":"имя документа"},"which":{"type":"string","description":"какая версия"}},"required":["q","which"]}`)},
}

// listVersions answers the versions tool.
func (r *toolRun) listVersions(ctx context.Context, q string) string {
	fams, err := r.versions.Families(ctx, q)
	if err != nil {
		return "ошибка: " + versions.Explain(err)
	}
	if len(fams) == 0 {
		return "ничего не найдено"
	}
	var b strings.Builder
	for i, f := range fams {
		if i == maxFamilies {
			fmt.Fprintf(&b, "…и еще документов: %d, уточните имя\n", len(fams)-maxFamilies)
			break
		}
		fmt.Fprintf(&b, "%s\n", f.Name)
		for n, v := range f.Versions {
			fmt.Fprintf(&b, "  %d. %s\n", n+1, versionLine(v))
		}
	}
	return b.String()
}

func versionLine(v VersionInfo) string {
	line := v.Date + " · " + v.Path
	if v.Note != "" {
		line += " · " + trimRunes(v.Note, searchSnip)
	}
	if v.Mark != "" {
		line += " · заметка: " + trimRunes(v.Mark, searchSnip)
	}
	if v.Current {
		line += " · актуальная"
	}
	return line
}

// openVersion answers open_version: одна версия читается, при сомнении модель
// получает список и вопрос к человеку, а не догадку.
func (r *toolRun) openVersion(ctx context.Context, q, which string) string {
	fams, err := r.versions.Families(ctx, q)
	if err != nil {
		return "ошибка: " + versions.Explain(err)
	}
	switch {
	case len(fams) == 0:
		return "ничего не найдено"
	case len(fams) > 1:
		var names []string
		for _, f := range fams {
			names = append(names, f.Name)
		}
		return "найдено несколько документов, спроси человека, какой нужен: " + strings.Join(names, "; ")
	case strings.TrimSpace(which) == "":
		return "не сказано, какую именно версию; спроси человека. Есть:\n" + r.listVersions(ctx, fams[0].Name)
	}
	f := fams[0]
	picked := versions.Pick(choices(f.Versions), which)
	switch {
	case len(picked) == 0:
		return "такой версии нет, спроси человека. Есть:\n" + r.listVersions(ctx, f.Name)
	case len(picked) > 1:
		var b strings.Builder
		b.WriteString("подходят несколько версий, спроси человека, какая нужна:\n")
		for _, i := range picked {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, versionLine(f.Versions[i]))
		}
		return b.String()
	}
	v := f.Versions[picked[0]]
	text, err := r.versions.Text(ctx, v.Path)
	if err != nil {
		return "ошибка чтения версии " + v.Path + ": " + versions.Explain(err)
	}
	if strings.TrimSpace(text) == "" {
		return "у версии " + v.Path + " нет текста (скан без распознавания или пустой файл)"
	}
	title := f.Name + " · " + v.Date
	r.read = appendNew(r.read, Doc{Ref: v.Path, Title: title, Source: "версии", Text: text})
	return title + "\n" + trimRunes(text, MaxReadRunes)
}

func choices(vs []VersionInfo) []versions.Choice {
	out := make([]versions.Choice, len(vs))
	for i, v := range vs {
		out[i] = versions.Choice{Path: v.Path, Date: v.Date, Note: v.Note, Mark: v.Mark, Current: v.Current}
	}
	return out
}
