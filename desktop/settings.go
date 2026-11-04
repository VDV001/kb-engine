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
	"sync/atomic"

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
	stateOK       = "ok"       // читается: счет файлов или записей
	stateMissing  = "missing"  // включен, но папки или файла сейчас нет (сеть, диск, доступ)
	stateOff      = "off"      // выключен человеком
	stateIndexing = "indexing" // доступен, но в индекс еще не вошел: первая сборка или папка вернулась
	statePending  = "pending"  // сервер: клиента пока нет, источник хранится
)

// sourceRow is one line of the sources screen.
type sourceRow struct {
	desktopconfig.Source
	State string `json:"state"`
	Count int    `json:"count"`
	// HasToken — для сервера: токен лежит в Связке ключей. Сам токен на
	// страницу не уходит никогда.
	HasToken bool   `json:"hasToken,omitempty"`
	Note     string `json:"note,omitempty"`
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
	// tokens и probe — Связка ключей и опрос kb-сервера (servers.go).
	tokens tokenStore
	probe  probeFunc
	// find — поиск на kb-сервере для общей выдачи (search.go).
	find findFunc
	// snap — снимок настроек для поиска: читается без f.mu, поэтому
	// сохранение, ждущее Связку под f.mu, не держит выдачу. SetSources
	// публикует его дважды: «сохраняется» перед записью токенов и итог в
	// конце, — поиск сверяет указатель (search.go).
	snap atomic.Pointer[cfgSnapshot]
	// open — папки, открытые в индексе (то же, что applied), для строки
	// источников без f.mu. Отдельно от snap: возврат сетевой папки (reconcile)
	// не трогает токены и не должен отменять идущий поиск по серверам.
	open atomic.Pointer[[]string]
	// countMatches — сколько файлов каждой папки нашел запрос (filesearch).
	countMatches func(q string, roots []string) (map[string]int, error)
	// indexing — индекс файлов сейчас пересобирается: счет папок старый, и
	// только что добавленная папка в нем еще пуста.
	indexing func() bool
	// applied — папки, открытые в индексе сейчас. Сохраненная папка, что была
	// недоступна при сохранении, сюда не входит, пока reconcile ее не откроет.
	applied []string
}

// GetSources returns the sources with their state and counts.
func (f *folderSettings) GetSources() sourcesView {
	f.mu.Lock()
	cfg, applied := f.cfg, slices.Clone(f.applied)
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
			if keys[i] = f.folderRow(r, applied); keys[i] != "" {
				ready = append(ready, keys[i])
			}
		case s.Kind == desktopconfig.KindCatalog:
			f.catalogRow(r, cfg.CatalogPath())
		case s.Kind == desktopconfig.KindServer:
			r.State = stateChecking // состояние догружает ServerStates
		default:
			r.State, r.Note = statePending, "вид источника из более новой версии kb"
		}
	}
	counts := f.countFiles(ready)
	for i := range rows {
		switch {
		case keys[i] == "":
		case counts == nil: // индекс еще не собран: счет неизвестен, а не ноль
			rows[i].State = stateIndexing
		default:
			rows[i].Count = counts[keys[i]]
		}
	}
	return sourcesView{Sources: rows, Exclude: slices.Clone(cfg.Exclude)}
}

// folderRow fills the state of a folder row and returns the expanded path to
// count files under, or "" when the folder is not in the index right now.
func (f *folderSettings) folderRow(r *sourceRow, applied []string) string {
	ok, skipped := desktopconfig.NormalizeRoots([]string{r.Path}, f.home)
	if len(ok) == 0 {
		r.State, r.Note = stateMissing, strings.Join(skipped, "; ")
		return ""
	}
	if !slices.Contains(applied, ok[0]) {
		r.State, r.Note = stateIndexing, "папка снова доступна — войдет в индекс в ближайший проход"
		return ""
	}
	r.State = stateOK
	return ok[0]
}

// catalogRow fills the state of a catalog row: читается только первый
// включенный каталог, остальные ждут слияния выдачи.
func (f *folderSettings) catalogRow(r *sourceRow, current string) {
	if r.Path != current {
		r.State, r.Note = statePending, "читается только первый каталог списка"
		return
	}
	n, err := f.countCatalog()
	if err != nil {
		r.State, r.Note = stateMissing, err.Error()
		return
	}
	r.State, r.Count = stateOK, n
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
func (f *folderSettings) SetSources(list []desktopconfig.Source, exclude []string, tokens map[string]string) error {
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
	catalog, err := f.checkCatalogs(next)
	if err != nil {
		return err
	}
	next.Exclude = dedupeFold(exclude, true)

	// Токены — до apply: отказ Связки ключей не должен оставить индекс на новых
	// источниках при старом конфиге. Откат токенов — при любом отказе дальше.
	if err := checkTokenIDs(next, tokens); err != nil {
		return err
	}
	// С этого места токены в Связке могут не совпадать с адресами снимка:
	// поиск пропускает серверы, пока сохранение не опубликует итог. Отказ
	// раньше ничего не трогал и поиск не задевает.
	f.publishLocked(true)
	defer f.publishLocked(false) // итог — новый конфиг или прежний при отказе; до Unlock
	undoTokens, err := f.storeTokens(next, tokens)
	if err != nil {
		return err
	}
	if err := f.apply(roots, next.Exclude, catalog); err != nil {
		undoTokens()
		return fmt.Errorf("%w: %w", errBadFolder, err)
	}
	if err := desktopconfig.Write(f.path, next); err != nil {
		undoTokens()
		// Индекс уже на новых источниках, а конфиг нет: окно работает, но
		// следующий запуск вернется к прежним. Говорим об этом прямо.
		return fmt.Errorf("источники применены, но не сохранены (вернутся прежние после перезапуска): %w", err)
	}
	if len(missing) > 0 {
		log.Printf("files: папки без связи, не индексируются: %s", strings.Join(missing, "; "))
	}
	f.dropTokens(f.cfg, next, tokens)
	f.cfg, f.applied = next, roots
	f.setOpen(roots)
	return nil
}

// checkCatalogs checks every enabled catalog of next and returns the path of
// the first, the one the window's other views read. Поиск читает их все, и
// опечатка во втором иначе всплыла бы бедой на каждой букве, а не отказом при
// сохранении. Уже сохраненный каталог, что сейчас недоступен (сетевой диск),
// не мешает.
func (f *folderSettings) checkCatalogs(next desktopconfig.Config) (string, error) {
	// Снисхождение «сохранен, сейчас недоступен» — только включенному: его
	// читали и видели. Выключенный мог быть сохранен без проверки (0.30.0
	// проверял только первый), и при включении проверяется как новый.
	saved := map[string]bool{} // по раскрытому пути: «~/…» и полный путь — один каталог
	for _, s := range f.cfg.WithSources().Sources {
		if s.Kind == desktopconfig.KindCatalog && s.Enabled {
			saved[desktopconfig.ExpandHome(s.Path, f.home)] = true
		}
	}
	first := ""
	for _, s := range next.Sources {
		if s.Enabled && s.Kind == desktopconfig.KindCatalog {
			p, err := f.catalogFile(s.Path, saved[desktopconfig.ExpandHome(s.Path, f.home)])
			if err != nil {
				return "", err
			}
			if first == "" {
				first = p
			}
		}
	}
	return first, nil
}

// reconcile reopens the folders when the set of enabled folders that can be
// opened now differs from the one in the index: сохраненная папка вернулась
// (VPN, диск). Зовется перед плановой пересборкой и при открытии экрана
// источников. Пустой набор не применяется никогда: пропали все папки —
// индекс остается прежним, а не затирается. Возвращает, применил ли.
func (f *folderSettings) reconcile() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	roots, _ := desktopconfig.NormalizeRoots(f.cfg.WithSources().FolderRoots(), f.home)
	if len(roots) == 0 || slices.Equal(roots, f.applied) {
		return false
	}
	catalog := desktopconfig.ExpandHome(f.cfg.WithSources().CatalogPath(), f.home)
	if err := f.apply(roots, f.cfg.Exclude, catalog); err != nil {
		log.Printf("files: вернувшиеся папки не открылись: %v", err)
		return false
	}
	f.applied = roots
	f.setOpen(roots)
	return true
}

// setOpen publishes the folders open in the index for the search; f.mu held.
func (f *folderSettings) setOpen(roots []string) {
	c := slices.Clone(roots)
	f.open.Store(&c)
}

// openableFolders returns the enabled folders to open, and the ones already in
// the config that are gone right now (их не открывают, но и не отклоняют).
func (f *folderSettings) openableFolders(next desktopconfig.Config) (roots, missing []string, err error) {
	known := map[string]bool{}
	for _, r := range f.cfg.WithSources().FolderRoots() {
		known[desktopconfig.ExpandHome(r, f.home)] = true
	}
	enabled := 0
	seen := map[string]string{}
	for _, s := range next.Sources {
		if s.Kind != desktopconfig.KindFolder {
			continue
		}
		// Одна папка двумя записями (~/notes и /Users/x/notes) ушла бы в
		// индекс и в MCP дважды — включенная или нет, это одна папка.
		key := desktopconfig.ExpandHome(s.Path, f.home)
		if prev, dup := seen[key]; dup {
			return nil, nil, fmt.Errorf("%w: %s и %s — одна и та же папка", errBadFolder, prev, s.Path)
		}
		seen[key] = s.Path
		if !s.Enabled {
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
	if len(roots) == 0 {
		// Включены только сохраненные, но сейчас недоступные папки: сохранение
		// затерло бы индекс пустым.
		return nil, nil, fmt.Errorf("%w: ни одна включенная папка сейчас недоступна", errNoFolders)
	}
	return roots, missing, checkNested(roots)
}

// catalogFile expands the catalog path and checks the file is there; "" — базы
// нет. Сохраненный каталог (known), которого сейчас нет, не отказ — как и папка:
// вкладка базы покажет ошибку чтения, пока файл не вернется.
func (f *folderSettings) catalogFile(path string, known bool) (string, error) {
	if path == "" {
		return "", nil
	}
	path = desktopconfig.ExpandHome(path, f.home)
	if info, err := os.Stat(path); (err != nil && !known) || (err == nil && info.IsDir()) {
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
