package main

import (
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
)

// problem is one thing a source cannot do, as the «Проблемы» panel shows it
// (#456): у сервера, каталога, папки и файла одна форма — чем конкретнее
// причина и действие, тем меньше человеку гадать.
type problem struct {
	Source string `json:"source"`           // id источника; "" — папки целиком (индекс, файл вне списка)
	Level  string `json:"level"`            // levelError, levelWarn или levelInfo
	Name   string `json:"name"`             // подпись источника, как в списке
	What   string `json:"what"`             // что не так, словами человека
	Todo   string `json:"todo,omitempty"`   // что сделать
	Path   string `json:"path,omitempty"`   // файл, если беда в одном файле
	Detail string `json:"detail,omitempty"` // исходная ошибка: по ней видно, что именно
}

// problemsView is the panel and the count in the status line.
type problemsView struct {
	// Total считает все, Problems — названные: нечитаемых файлов статус
	// несет первые 100, а Skipped считает все.
	Total    int       `json:"total"`
	Level    string    `json:"level,omitempty"` // худший уровень; "" — проблем нет
	Problems []problem `json:"problems"`
}

// Уровни проблем — цвет точки в строке статуса: красная, оранжевая, синяя.
const (
	levelError = "error" // источник не читается, файла нет в поиске
	levelWarn  = "warn"  // файл в поиске, но только по имени
	levelInfo  = "info"  // окно повторит само
)

// levelRank orders the levels; "" — ниже всех.
func levelRank(l string) int {
	switch l {
	case levelError:
		return 3
	case levelWarn:
		return 2
	case levelInfo:
		return 1
	}
	return 0
}

// worse returns the more serious of two levels.
func worse(a, b string) string {
	if levelRank(b) > levelRank(a) {
		return b
	}
	return a
}

// Причины окна в той же форме «что не так — что сделать», что Explain и Describe.
const (
	keychainNote      = keychain.Locked + " и откройте панель снова"
	recognizeFailed   = "PDF или картинка не распознались, файл ищется только по имени — откройте его: если он цел, распознавание повторится, когда файл изменится"
	recognizeTimedOut = "распознавание не уложилось во время — окно попробует снова не раньше чем через час"
	foldersName       = "Папки"
)

// splitReason splits «что не так — что сделать».
func splitReason(s string) (what, todo string) {
	what, todo, _ = strings.Cut(s, " — ")
	return what, todo
}

// collectProblems gathers the problems of the sources and of the file index.
// rows — строки источников с живым состоянием серверов (ServerStates).
func collectProblems(rows []sourceRow, st indexStatus, home string) problemsView {
	out := problemsView{Problems: []problem{}}
	var folders []sourceRow
	for _, r := range rows {
		if r.Kind == desktopconfig.KindFolder && r.Enabled {
			folders = append(folders, r)
		}
		if r.State != stateMissing {
			continue
		}
		p := problem{Source: r.ID, Level: levelError, Name: sourceLabel(r.Source), Detail: r.Note}
		switch r.Kind {
		case desktopconfig.KindFolder:
			p.What, p.Todo = "папка недоступна", "подключите диск или VPN, или поправьте путь в «Источниках»"
		case desktopconfig.KindCatalog:
			p.What, p.Todo = "каталог не читается", "проверьте файл catalog.json и путь к нему в «Источниках»"
		default: // сервер: Note уже словами человека (Describe), исходная ошибка — в Detail
			p.What, p.Todo = splitReason(r.Note)
			p.Detail = r.Detail
		}
		out.Problems = append(out.Problems, p)
	}
	if st.Error != "" {
		out.Problems = append(out.Problems, problem{Level: levelError, Name: foldersName, What: "индекс файлов не собрался",
			Todo: "проверьте папки в «Источниках» и сохраните их снова", Detail: st.Error})
	}
	out.Total = len(out.Problems) + st.Skipped + max(len(st.Unrecognized), st.UnrecognizedCount)
	for i, f := range slices.Concat(st.SkippedFiles, st.Unrecognized) {
		p := problem{Level: levelError, Name: foldersName, Path: f.Path, Detail: f.Detail}
		switch {
		case i < len(st.SkippedFiles): // нечитаемый файл не ищется даже по имени
		case f.Reason == recognizeTimedOut:
			p.Level = levelInfo
		default:
			p.Level = levelWarn
		}
		p.What, p.Todo = splitReason(f.Reason)
		if r, ok := folderOf(folders, f.Path, home); ok {
			p.Source, p.Name = r.ID, sourceLabel(r.Source)
		}
		out.Problems = append(out.Problems, p)
	}
	for _, p := range out.Problems {
		out.Level = worse(out.Level, p.Level)
	}
	if st.Skipped > 0 { // файлы сверх названных
		out.Level = worse(out.Level, levelError)
	}
	if st.UnrecognizedCount > len(st.Unrecognized) {
		out.Level = worse(out.Level, levelInfo) // уровень названных уже учтен
	}
	return out
}

// folderOf finds the folder a file belongs to: самая глубокая из папок, что
// его содержат (папка внутри папки — отдельный источник).
func folderOf(folders []sourceRow, path, home string) (sourceRow, bool) {
	var best sourceRow
	n := -1
	for _, r := range folders {
		root := filepath.Clean(desktopconfig.ExpandHome(r.Path, home))
		if (path == root || strings.HasPrefix(path, root+string(filepath.Separator))) && len(root) > n {
			best, n = r, len(root)
		}
	}
	return best, n >= 0
}

// sourceLabel names a source the way the list does (label в app.js).
func sourceLabel(s desktopconfig.Source) string {
	switch {
	case s.Name != "":
		return s.Name
	case s.Kind == desktopconfig.KindServer:
		if u, err := url.Parse(s.URL); err == nil && u.Host != "" {
			return u.Host
		}
		return s.URL
	default:
		return filepath.Base(s.Path)
	}
}
