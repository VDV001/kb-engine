package mcpserver

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daniil/kb-engine/internal/usecase/versions"
)

// VersionFinder gives the agent the iterations of documents (#547): те же
// семейства, подписи «что нового» и заметки, что на вкладке «Артефакты» окна.
// Только чтение: заметки и значки агент не ставит.
type VersionFinder interface {
	// Families — семейства по запросу, версии в каждом от старой к новой.
	Families(ctx context.Context, q string) ([]VersionFamily, error)
	// Text — текст версии по пути из Families.
	Text(ctx context.Context, path string) (string, error)
}

// VersionFamily — документ и его версии.
type VersionFamily struct {
	Name     string
	Versions []VersionEntry
}

// VersionEntry — одна версия.
type VersionEntry struct {
	Path, Date, Note, Mark string
	Size                   int64
	Current                bool
}

// unreadable — версии не прочитались. Своя ошибка вместо настоящей: в той бывает
// полный путь (имя пользователя) и кусок текста, а все, что получает агент,
// уходит провайдеру модели. Причина названа словами (versions.Explain).
func unreadable(err error) error { return errors.New(versions.Explain(err)) }

const (
	// maxVersionFamilies — сколько документов отдает один search_versions.
	maxVersionFamilies = 5
	// maxVersionRunes — предел текста версии, как у read_file.
	maxVersionRunes = 8000
	maxNoteRunes    = 300
)

type searchVersionsArgs struct {
	Query string `json:"query" jsonschema:"имя документа или его часть; пустой — документы с несколькими версиями"`
}

type openVersionArgs struct {
	Query string `json:"query" jsonschema:"имя документа"`
	Which string `json:"which" jsonschema:"какая версия, словами человека: последняя, предыдущая, первая, v3, от 05.11, актуальная или слова из его заметки"`
}

// versionJSON is one version as the agent sees it.
type versionJSON struct {
	N       int    `json:"n"`
	Date    string `json:"date"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Note    string `json:"note"`
	Mark    string `json:"mark,omitempty"`
	Current bool   `json:"current,omitempty"`
}

func versionList(vs []VersionEntry) []versionJSON {
	out := make([]versionJSON, len(vs))
	for i, v := range vs {
		out[i] = versionJSON{N: i + 1, Date: v.Date, Path: v.Path, Size: v.Size, Note: clip(v.Note, maxNoteRunes), Mark: clip(v.Mark, maxNoteRunes), Current: v.Current}
	}
	return out
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// addVersionTools registers search_versions and open_version over f: чтение
// без записи, те же списки, что видит человек на вкладке «Артефакты».
func addVersionTools(s *mcp.Server, j journal, f VersionFinder) {
	addToolCtx(s, j, &mcp.Tool{
		Name: "search_versions",
		Description: "Версии (итерации) документов человека: по каждой дата, путь, что в нее добавили или убрали относительно предыдущей, " +
			"заметка человека («отправлено Алексею») и значок «актуальная». Ищет по имени документа. Текста файлов здесь нет: его отдает open_version.",
	}, func(in searchVersionsArgs) []string {
		if in.Query == "" {
			return nil
		}
		return []string{in.Query}
	}, func(ctx context.Context, in searchVersionsArgs) (map[string]any, error) {
		fams, err := f.Families(ctx, in.Query)
		if err != nil {
			return nil, unreadable(err)
		}
		type famJSON struct {
			Name     string        `json:"name"`
			Versions []versionJSON `json:"versions"`
		}
		out := make([]famJSON, 0, min(len(fams), maxVersionFamilies))
		for _, fam := range fams[:min(len(fams), maxVersionFamilies)] {
			out = append(out, famJSON{Name: fam.Name, Versions: versionList(fam.Versions)})
		}
		return map[string]any{"found": len(fams), "families": out}, nil
	})
	addToolCtx(s, j, &mcp.Tool{
		Name: "open_version",
		Description: "Текст одной версии документа. which — словами человека: последняя, предыдущая, первая, v3, от 05.11, актуальная или слова из его заметки. " +
			"Если подходит несколько версий или документов, в ответе поле ask и кандидаты: спроси человека, не угадывай. Текст уходит провайдеру модели.",
	}, func(in openVersionArgs) []string { return []string{in.Query, in.Which} },
		func(ctx context.Context, in openVersionArgs) (map[string]any, error) {
			fams, err := f.Families(ctx, in.Query)
			if err != nil {
				return nil, unreadable(err)
			}
			return openVersion(ctx, f, fams, in)
		})
}

// openVersion picks one version by the words or answers with what to ask.
func openVersion(ctx context.Context, f VersionFinder, fams []VersionFamily, in openVersionArgs) (map[string]any, error) {
	switch {
	case len(fams) == 0:
		return map[string]any{"ask": "документ не найден: уточните имя"}, nil
	case len(fams) > 1:
		names := make([]string, len(fams))
		for i, fam := range fams {
			names[i] = fam.Name
		}
		return map[string]any{"ask": "найдено несколько документов, спросите, какой нужен", "candidates": names}, nil
	case strings.TrimSpace(in.Which) == "":
		return map[string]any{"ask": "не сказано, какая версия, спросите", "candidates": versionList(fams[0].Versions)}, nil
	}
	fam := fams[0]
	choices := make([]versions.Choice, len(fam.Versions))
	for i, v := range fam.Versions {
		choices[i] = versions.Choice{Path: v.Path, Date: v.Date, Note: v.Note, Mark: v.Mark, Current: v.Current}
	}
	picked := versions.Pick(choices, in.Which)
	if len(picked) != 1 {
		return askWhich(fam, picked), nil
	}
	v := fam.Versions[picked[0]]
	text, err := f.Text(ctx, v.Path)
	if err != nil {
		return nil, unreadable(err)
	}
	return map[string]any{"name": fam.Name, "date": v.Date, "path": v.Path, "text": clip(text, maxVersionRunes)}, nil
}

// askWhich answers with the versions to choose from: подходящие, а если
// таких нет, вся лента.
func askWhich(fam VersionFamily, picked []int) map[string]any {
	if len(picked) == 0 {
		return map[string]any{"ask": "такой версии нет, спросите, какая нужна", "candidates": versionList(fam.Versions)}
	}
	cands := make([]VersionEntry, len(picked))
	for i, k := range picked {
		cands[i] = fam.Versions[k]
	}
	return map[string]any{"ask": "подходят несколько версий, спросите, какая нужна", "candidates": versionList(cands)}
}
