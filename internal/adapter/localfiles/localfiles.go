// Package localfiles walks folders on this machine and reads text files for
// the file index. It is also the trust boundary for reading: a path that comes
// from outside (an MCP client) is read only if the walk would have indexed it.
package localfiles

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// MaxFileBytes — файлы больше 2 МБ не индексируются: текст такого размера —
// почти всегда выгрузка или бэкап, а не то, что человек ищет, и он раздул бы
// индекс сильнее всех остальных файлов вместе.
const MaxFileBytes = 2 << 20

// ErrNotAllowed is returned when a path is outside the roots or is a file the
// walk never indexes (secrets, junk folders, binaries). It is the port's error,
// so HTTP and MCP map it without knowing this adapter.
var ErrNotAllowed = filesearch.ErrNotAllowed

// junkDirs — служебные папки инструментов: в них нет того, что ищет человек,
// а по объему они больше всего остального.
var junkDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, "venv": true,
	"__pycache__": true, ".next": true, ".turbo": true, "dist": true,
	"target": true, ".cache": true, ".idea": true,
}

// isSecret — файлы с ключами и переменными окружения. Их нет в индексе и их
// нельзя прочитать через ReadText: индекс отдается агенту по MCP, и секрет в
// нем был бы секретом, отданным наружу.
func isSecret(name string) bool {
	switch {
	case name == ".env", strings.HasPrefix(name, ".env."):
		return true
	case strings.HasPrefix(name, "id_rsa"), strings.HasPrefix(name, "id_ed25519"), strings.HasPrefix(name, "id_ecdsa"):
		return true
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pem", ".key", ".p12", ".pfx", ".keystore":
		return true
	}
	return false
}

// binaryDocs — документы, чей текст простым чтением не достать.
//
// ponytail: PDF и DOCX пропускаются целиком. Потолок: в Загрузках это главные
// документы. Путь: извлечение текста (pdftotext / разбор docx) отдельным
// шагом вместе с OCR картинок.
var binaryDocs = map[string]bool{".pdf": true, ".docx": true, ".rtf": true}

func isIndexable(name string) bool {
	if isSecret(name) || strings.Contains(name, ".bak") {
		return false
	}
	k := domain.FileKindOf(name)
	if k != domain.FileKindDoc && k != domain.FileKindCode {
		return false
	}
	return !binaryDocs[strings.ToLower(filepath.Ext(name))]
}

// Source walks roots and reads their text files.
type Source struct {
	roots    []string // как заданы, для обхода
	resolved []string // с раскрытыми симлинками, для проверки чтения
	exclude  map[string]bool
}

// New returns a source over absolute roots. excludeDirs are folder names that
// are never entered, in addition to the built-in junk folders.
func New(roots, excludeDirs []string) (*Source, error) {
	s := &Source{exclude: map[string]bool{}}
	for _, r := range roots {
		if !filepath.IsAbs(r) {
			return nil, fmt.Errorf("root must be absolute: %q", r)
		}
		r = filepath.Clean(r)
		res, err := filepath.EvalSymlinks(r)
		if err != nil {
			return nil, fmt.Errorf("resolve root %q: %w", r, err)
		}
		s.roots = append(s.roots, r)
		s.resolved = append(s.resolved, res)
	}
	for _, d := range excludeDirs {
		s.exclude[d] = true
	}
	return s, nil
}

func (s *Source) skipDir(name string) bool { return junkDirs[name] || s.exclude[name] }

// Walk calls fn for every indexable regular file under the roots. Symlinks are
// not followed: ссылка может увести за пределы корня.
func (s *Source) Walk(fn func(domain.FileDoc) error) error {
	for _, root := range s.roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // нечитаемая папка не роняет обход остальных
			}
			if d.IsDir() {
				if path != root && s.skipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || !isIndexable(d.Name()) {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Size() > MaxFileBytes {
				return nil
			}
			doc, err := domain.NewFileDoc(path, info.Size(), info.ModTime())
			if err != nil {
				return err
			}
			return fn(doc)
		})
		if err != nil {
			return fmt.Errorf("walk %s: %w", root, err)
		}
	}
	return nil
}

// ReadText returns a file's text if the walk would have indexed it.
//
// Путь приходит снаружи (HTTP, MCP), поэтому файловая система трогается
// только через os.Root корня: даже если проверка пропустит что-то, открыть
// файл за пределами корня Root не даст. Прежняя версия проверяла путь с
// раскрытыми симлинками, а читала исходный — между проверкой и чтением ссылку
// можно было подменить (замечание CodeQL go/path-injection на PR #371).
func (s *Source) ReadText(path string) (string, error) {
	dir, rel, ok := s.locate(path)
	if !ok || s.throughSkipped(rel) || !isIndexable(filepath.Base(rel)) {
		return "", fmt.Errorf("%w: %q", ErrNotAllowed, path)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // только чтение: ошибка закрытия ничего не теряет
	if !plainFile(root, rel) {
		return "", fmt.Errorf("%w: %q", ErrNotAllowed, path)
	}
	b, err := root.ReadFile(rel)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("not UTF-8 text: %q", path)
	}
	return string(b), nil
}

// locate finds the root a path lies under, lexically, and returns that root
// and the path relative to it. Both spellings of a root count: as configured
// and with symlinks resolved (on macOS /var is /private/var).
func (s *Source) locate(path string) (dir, rel string, ok bool) {
	if !filepath.IsAbs(path) {
		return "", "", false
	}
	clean := filepath.Clean(path)
	for i := range s.roots {
		for _, root := range []string{s.roots[i], s.resolved[i]} {
			if rel, ok := inside(root, clean); ok && rel != "." {
				return s.resolved[i], rel, true
			}
		}
	}
	return "", "", false
}

// plainFile reports whether rel is a regular file of indexable size reached
// without a single symlink. Обход ссылки не индексирует, поэтому и чтение по
// ним — это обход проверки имени, а не найденный в выдаче файл.
func plainFile(root *os.Root, rel string) bool {
	parts := strings.Split(rel, string(filepath.Separator))
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if i == len(parts)-1 {
			return info.Mode().IsRegular() && info.Size() <= MaxFileBytes
		}
	}
	return false
}

// inside reports whether path lies under root and returns it relative to root.
func inside(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// throughSkipped reports whether a relative path passes through a folder the
// walk never enters.
func (s *Source) throughSkipped(rel string) bool {
	for d := range strings.SplitSeq(filepath.Dir(rel), string(filepath.Separator)) {
		if d != "." && s.skipDir(d) {
			return true
		}
	}
	return false
}
