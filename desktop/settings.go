package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Ошибки выбора папок: окно показывает текст, а тесты и вызывающие проверяют
// errors.Is, а не подстроку.
var (
	errNoFolders  = errors.New("нужна хотя бы одна включенная папка")
	errBadFolder  = errors.New("не подходит")
	errBadCatalog = errors.New("файла каталога нет")
)

// Состояния источника на экране настроек.
const (
	stateOK      = "ok"      // читается: счет файлов или записей
	stateMissing = "missing" // включен, но папки или файла сейчас нет (сеть, диск, доступ)
	stateOff     = "off"     // выключен человеком
	statePending = "pending" // сервер: клиента пока нет, источник хранится
)

// sourceRow is one line of the sources screen.
type sourceRow struct {
	desktopconfig.Source
	State string `json:"state"`
	Count int    `json:"count"`
	Note  string `json:"note,omitempty"`
}

// sourcesView is what the sources screen shows and sends back.
type sourcesView struct {
	Sources []sourceRow `json:"sources"`
	Exclude []string    `json:"exclude"`
}

// folderSettings — откуда окно берет файлы и записи. Меняется только из окна
// (привязки Wails), а не через HTTP API: иначе любой, кто достучался до API,
// мог бы направить индекс на чужие папки.
type folderSettings struct {
	mu   sync.Mutex
	path string // config.json
	home string
	cfg  desktopconfig.Config
	// apply открывает папки, переключает каталог и запускает переиндексацию.
	apply func(roots, exclude []string, catalog string) error
	// countFiles и countCatalog — счет для экрана: файлы по папкам из индекса,
	// записи текущего каталога.
	countFiles   func(roots []string) map[string]int
	countCatalog func() (int, error)
}

// GetSources returns the sources with their state and counts.
func (f *folderSettings) GetSources() sourcesView {
	f.mu.Lock()
	cfg := f.cfg
	f.mu.Unlock()
	list := cfg.Sources
	if len(list) == 0 {
		list = cfg.WithSources().Sources
	}
	rows := make([]sourceRow, len(list))
	keys := make([]string, len(list)) // раскрытый путь папки — ключ счета
	var ready []string
	for i, s := range list {
		rows[i] = sourceRow{Source: s}
		r := &rows[i]
		switch {
		case !s.Enabled:
			r.State = stateOff
		case s.Kind == desktopconfig.KindFolder:
			ok, skipped := desktopconfig.NormalizeRoots([]string{s.Path}, f.home)
			if len(ok) == 0 {
				r.State, r.Note = stateMissing, strings.Join(skipped, "; ")
				continue
			}
			r.State, keys[i] = stateOK, ok[0]
			ready = append(ready, ok[0])
		case s.Kind == desktopconfig.KindCatalog:
			if s.Path != cfg.CatalogPath() {
				// Читается первый включенный каталог; остальные ждут слияния выдачи.
				r.State, r.Note = statePending, "читается только первый каталог списка"
				continue
			}
			n, err := f.countCatalog()
			if err != nil {
				r.State, r.Note = stateMissing, err.Error()
				continue
			}
			r.State, r.Count = stateOK, n
		case s.Kind == desktopconfig.KindServer:
			r.State, r.Note = statePending, "клиент сервера появится в следующей версии"
		default:
			r.State, r.Note = statePending, "вид источника из более новой версии kb"
		}
	}
	counts := f.countFiles(ready)
	for i := range rows {
		if keys[i] != "" {
			rows[i].Count = counts[keys[i]]
		}
	}
	return sourcesView{Sources: rows, Exclude: slices.Clone(cfg.Exclude)}
}

// SetSources validates the list, applies it and only then saves it.
//
// Порядок важен. Сначала проверяется все: одна плохая строка отклоняет список
// с ее названием, ничего не меняя. Потом папки открываются и каталог
// переключается (apply), и только если это удалось, пишется конфиг: иначе
// папка, которую macOS не дает открыть, сохранялась бы, и следующий запуск
// окна падал.
//
// Новая или измененная папка обязана существовать — так ловится опечатка.
// Папка, что уже была в конфиге, может временно пропасть (сетевой диск без
// VPN): она остается в списке «нет связи» и не мешает переставить соседей.
func (f *folderSettings) SetSources(list []desktopconfig.Source, exclude []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	next := f.cfg
	next.Sources = slices.Clone(list)
	next.Roots, next.Catalog, next.Server = nil, "", ""
	next.AssignIDs()
	if err := next.Validate(); err != nil {
		return err
	}
	roots, missing, err := f.openableFolders(next)
	if err != nil {
		return err
	}
	catalog, err := f.catalogFile(next.CatalogPath())
	if err != nil {
		return err
	}
	next.Exclude = dedupeFold(exclude, true)

	if err := f.apply(roots, next.Exclude, catalog); err != nil {
		return fmt.Errorf("%w: %w", errBadFolder, err)
	}
	if err := desktopconfig.Write(f.path, next); err != nil {
		// Индекс уже на новых источниках, а конфиг нет: окно работает, но
		// следующий запуск вернется к прежним. Говорим об этом прямо.
		return fmt.Errorf("источники применены, но не сохранены (вернутся прежние после перезапуска): %w", err)
	}
	if len(missing) > 0 {
		log.Printf("files: папки без связи, не индексируются: %s", strings.Join(missing, "; "))
	}
	f.cfg = next
	return nil
}

// openableFolders returns the enabled folders to open, and the ones already in
// the config that are gone right now (их не открывают, но и не отклоняют).
func (f *folderSettings) openableFolders(next desktopconfig.Config) (roots, missing []string, err error) {
	known := map[string]bool{}
	for _, r := range f.cfg.WithSources().FolderRoots() {
		known[desktopconfig.ExpandHome(r, f.home)] = true
	}
	enabled := 0
	for _, s := range next.Sources {
		if !s.Enabled || s.Kind != desktopconfig.KindFolder {
			continue
		}
		enabled++
		ok, skipped := desktopconfig.NormalizeRoots([]string{s.Path}, f.home)
		switch {
		case len(ok) == 1:
			roots = append(roots, ok[0])
		case known[desktopconfig.ExpandHome(s.Path, f.home)]:
			missing = append(missing, s.Path)
		default:
			return nil, nil, fmt.Errorf("%w: %s", errBadFolder, strings.Join(skipped, "; "))
		}
	}
	if enabled == 0 {
		return nil, nil, errNoFolders
	}
	roots = dedupeFold(roots, false)
	return roots, missing, checkNested(roots)
}

// catalogFile expands the catalog path and checks the file is there; "" — базы нет.
func (f *folderSettings) catalogFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	path = desktopconfig.ExpandHome(path, f.home)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", fmt.Errorf("%w: %s", errBadCatalog, path)
	}
	return path, nil
}

// checkNested refuses a folder that lies inside another one: its files would be
// indexed twice and show up twice in results. Сравнение по раскрытым симлинкам.
func checkNested(roots []string) error {
	real := make([]string, len(roots))
	for i, r := range roots {
		real[i] = r
		if res, err := filepath.EvalSymlinks(r); err == nil {
			real[i] = res
		}
	}
	for i := range real {
		for j := range real {
			if i == j {
				continue
			}
			if rel, ok := inside(real[j], real[i]); ok {
				if rel == "." {
					return fmt.Errorf("%w: %s и %s — одна и та же папка", errBadFolder, roots[i], roots[j])
				}
				return fmt.Errorf("%w: %s уже внутри %s", errBadFolder, roots[i], roots[j])
			}
		}
	}
	return nil
}

func inside(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// dedupeFold trims, drops empty values and duplicates. With fold the
// comparison ignores case and the value is lowered — так исключения
// сравнивает адаптер (APFS регистр не различает).
func dedupeFold(values []string, fold bool) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if fold {
			v = strings.ToLower(v)
		}
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
