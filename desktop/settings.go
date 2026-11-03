package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Ошибки выбора папок: окно показывает текст, а тесты и вызывающие проверяют
// errors.Is, а не подстроку.
var (
	errNoFolders = errors.New("нужна хотя бы одна папка")
	errBadFolder = errors.New("не подходит")
)

// foldersView is what the settings panel shows and sends back.
type foldersView struct {
	Roots   []string `json:"roots"`
	Exclude []string `json:"exclude"`
	// Missing — папки из конфига, которых сейчас нет или к которым нет
	// доступа. Показываются отдельно: в список рабочих они не входят, и
	// сохранение не отказывает из-за папки, которую человек не добавлял.
	Missing []string `json:"missing"`
}

// folderSettings — какие папки индексируются. Меняется только из окна
// (привязки Wails), а не через HTTP API: иначе любой, кто достучался до API,
// мог бы направить индекс на чужие папки.
type folderSettings struct {
	mu    sync.Mutex
	path  string // config.json
	home  string
	cfg   desktopconfig.Config
	apply func(roots, exclude []string) error // открывает папки и запускает переиндексацию
}

// Get returns the folders being indexed, with unusable ones split out.
func (f *folderSettings) Get() foldersView {
	f.mu.Lock()
	defer f.mu.Unlock()
	roots, missing := desktopconfig.NormalizeRoots(f.cfg.FolderRoots(), f.home)
	return foldersView{Roots: roots, Exclude: slices.Clone(f.cfg.Exclude), Missing: missing}
}

// Set validates the folders, opens them and only then saves them.
//
// Порядок важен. Все папки проверяются до всего остального: одна плохая
// отклоняет список с ее названием, ничего не меняя. Потом папки открываются
// (apply), и только если это удалось, пишется конфиг: иначе папка, которую
// macOS не дает открыть, сохранялась бы, и следующий запуск окна падал.
func (f *folderSettings) Set(roots, exclude []string) error {
	clean, skipped := desktopconfig.NormalizeRoots(roots, f.home)
	if len(skipped) > 0 {
		return fmt.Errorf("%w: %s", errBadFolder, strings.Join(skipped, "; "))
	}
	clean = dedupeFold(clean, false)
	if len(clean) == 0 {
		return errNoFolders
	}
	if err := checkNested(clean); err != nil {
		return err
	}
	ex := dedupeFold(exclude, true)

	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.apply(clean, ex); err != nil {
		return fmt.Errorf("%w: %w", errBadFolder, err)
	}
	next := f.cfg
	next.SetFolderRoots(clean, f.home)
	next.Exclude = ex
	if err := desktopconfig.Write(f.path, next); err != nil {
		// Индекс уже на новых папках, а конфиг нет: окно работает, но
		// следующий запуск вернется к прежним папкам. Говорим об этом прямо.
		return fmt.Errorf("папки применены, но не сохранены (вернутся прежние после перезапуска): %w", err)
	}
	f.cfg = next
	return nil
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
