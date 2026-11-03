// Package localfiles walks folders on this machine and reads text files for
// the file index. It is also the trust boundary for reading: a path that comes
// from outside (an MCP client) is read only if the walk would have indexed it.
package localfiles

import (
	"context"
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

// Все сравнения имен — в нижнем регистре: APFS регистр не различает, и
// FINANCES/ledger.md открывает finances/ledger.md. Сравнение с учетом регистра
// пропускало исключенную папку (ревью #371, проверено живым замером).

// junkDirs — служебные папки инструментов: в них нет того, что ищет человек,
// а по объему они больше всего остального.
var junkDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, "venv": true,
	"__pycache__": true, ".next": true, ".turbo": true, "dist": true,
	"target": true, ".cache": true, ".idea": true,
}

// secretNames — файлы с доступами, известные по имени целиком.
var secretNames = map[string]bool{
	".env": true, ".envrc": true, ".npmrc": true, ".netrc": true, ".pgpass": true,
	".pypirc": true, "kubeconfig": true, ".git-credentials": true,
}

// secretWords — слова в имени, которые выдают файл с доступами: так их называют
// GCP (service-account.json), AWS (credentials) и сами люди (secrets.yaml).
var secretWords = []string{"secret", "credential", "service-account", "service_account"}

// configExts — форматы, в которых лежат доступы; пустое расширение — файлы
// вроде credentials без расширения.
var configExts = map[string]bool{
	"": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".conf": true, ".cfg": true, ".txt": true,
}

// isSecret — файлы с ключами и доступами. Их нет в индексе и их нельзя прочитать
// через ReadText: индекс отдается агенту по MCP, и секрет в нем был бы
// секретом, отданным наружу.
//
// ponytail: распознавание по имени. Потолок: токен внутри config.toml с
// безобидным именем пройдет. Путь: сканер содержимого (правила gitleaks) при
// индексации, если такие файлы начнут попадаться.
func isSecret(name string) bool {
	n := strings.ToLower(name)
	switch {
	case secretNames[n], strings.HasPrefix(n, ".env."):
		return true
	case strings.HasPrefix(n, "id_rsa"), strings.HasPrefix(n, "id_ed25519"), strings.HasPrefix(n, "id_ecdsa"):
		return true
	case strings.Contains(n, ".tfstate"):
		return true
	}
	// Слова в имени проверяются только у конфигов: заметка «чистка секретов»
	// в .md — не секрет, и на живой папке правило без этой оговорки срезало
	// именно такой handoff.
	if configExts[filepath.Ext(n)] {
		for _, w := range secretWords {
			if strings.Contains(n, w) {
				return true
			}
		}
	}
	switch filepath.Ext(n) {
	case ".pem", ".key", ".p12", ".pfx", ".keystore":
		return true
	}
	return false
}

// indexable reports whether the walk takes a file by its name. Docx читается
// разбором XML, pdf — через pdftotext, если он нашелся (docs.go).
//
// ponytail: RTF и текст на картинках не читаются. Потолок: сканы и скриншоты
// документов. Путь: OCR через Vision macOS отдельным шагом.
func (s *Source) indexable(name string) bool {
	if isSecret(name) || strings.Contains(strings.ToLower(name), ".bak") {
		return false
	}
	k := domain.FileKindOf(name)
	if k != domain.FileKindDoc && k != domain.FileKindCode {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".rtf":
		return false
	case ".pdf":
		return s.ReadsPDF()
	}
	return true
}

// Source walks roots and reads their text files.
type Source struct {
	roots    []string   // как заданы: путь снаружи может прийти в этом написании
	resolved []string   // с раскрытыми симлинками: по ним идет обход и чтение
	dirs     []*os.Root // открыты один раз, а не на каждое чтение
	exclude  map[string]bool
	// pdftotext — путь к программе, найденный при открытии; пусто — pdf не
	// индексируются и не читаются.
	pdftotext string
}

// New returns a source over absolute roots. excludeDirs are folder names that
// are never entered, in addition to the built-in junk folders. Close releases
// the roots.
func New(roots, excludeDirs []string) (*Source, error) {
	s := &Source{exclude: map[string]bool{}, pdftotext: findPdftotext()}
	for _, r := range roots {
		if !filepath.IsAbs(r) {
			_ = s.Close()
			return nil, fmt.Errorf("root must be absolute: %q", r)
		}
		r = filepath.Clean(r)
		res, err := filepath.EvalSymlinks(r)
		if err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("resolve root %q: %w", r, err)
		}
		dir, err := os.OpenRoot(res)
		if err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("open root %q: %w", r, err)
		}
		s.roots = append(s.roots, r)
		s.resolved = append(s.resolved, res)
		s.dirs = append(s.dirs, dir)
	}
	for _, d := range excludeDirs {
		s.exclude[strings.ToLower(d)] = true
	}
	return s, nil
}

// Close releases the opened roots.
func (s *Source) Close() error {
	for _, d := range s.dirs {
		_ = d.Close() // только чтение: ошибка закрытия ничего не теряет
	}
	s.dirs = nil
	return nil
}

func (s *Source) skipDir(name string) bool {
	n := strings.ToLower(name)
	return junkDirs[n] || s.exclude[n]
}

// Walk calls fn for every indexable regular file under the roots. Symlinks
// inside the roots are not followed: ссылка может увести за пределы корня.
// Сам корень обходится раскрытым: корень-симлинк иначе давал ноль файлов.
func (s *Source) Walk(ctx context.Context, fn func(domain.FileDoc) error) error {
	for i, root := range s.resolved {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			if err != nil {
				return nil // нечитаемая папка не роняет обход остальных
			}
			if d.IsDir() {
				if path != root && s.skipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			// В выдаче путь в том написании, в каком человек задал корень.
			return s.visitFile(s.roots[i]+strings.TrimPrefix(path, root), d, fn)
		})
		if err != nil {
			return fmt.Errorf("walk %s: %w", root, err)
		}
	}
	return nil
}

// visitFile passes one walked entry to fn if it is an indexable regular file.
func (s *Source) visitFile(shown string, d fs.DirEntry, fn func(domain.FileDoc) error) error {
	if !d.Type().IsRegular() || !s.indexable(d.Name()) {
		return nil
	}
	info, err := d.Info()
	if err != nil || info.Size() > maxBytes(d.Name()) {
		return nil
	}
	doc, err := domain.NewFileDoc(shown, info.Size(), info.ModTime())
	if err != nil {
		return err
	}
	return fn(doc)
}

// Check reports whether a path is one the walk would index, without reading
// it. Окно зовет ее перед open: читать файл целиком ради разрешения незачем.
func (s *Source) Check(path string) error {
	if _, _, err := s.checked(path); err != nil {
		return err
	}
	return nil
}

// ReadText returns a file's text if the walk would have indexed it.
//
// Путь приходит снаружи (HTTP, MCP), поэтому файловая система трогается
// только через os.Root корня: даже если проверка пропустит что-то, открыть
// файл за пределами корня Root не даст (замечание CodeQL go/path-injection).
func (s *Source) ReadText(path string) (string, error) {
	dir, rel, err := s.checked(path)
	if err != nil {
		return "", err
	}
	if ext := docExt(rel); ext != "" {
		return s.readDoc(dir, rel, ext)
	}
	b, err := dir.ReadFile(rel)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("not UTF-8 text: %q", path)
	}
	return string(b), nil
}

// checked runs every rule the walk applies and returns the root and the path
// relative to it.
func (s *Source) checked(path string) (*os.Root, string, error) {
	i, rel, ok := s.locate(path)
	if !ok || s.throughSkipped(rel) || !s.indexable(filepath.Base(rel)) || !plainFile(s.dirs[i], rel) {
		return nil, "", fmt.Errorf("%w: %q", ErrNotAllowed, path)
	}
	return s.dirs[i], rel, nil
}

// locate finds the root a path lies under, lexically, and returns its index and
// the path relative to it. Both spellings of a root count: as configured and
// with symlinks resolved (on macOS /var is /private/var).
func (s *Source) locate(path string) (int, string, bool) {
	if !filepath.IsAbs(path) {
		return 0, "", false
	}
	clean := filepath.Clean(path)
	for i := range s.roots {
		for _, root := range []string{s.roots[i], s.resolved[i]} {
			if rel, ok := inside(root, clean); ok && rel != "." {
				return i, rel, true
			}
		}
	}
	return 0, "", false
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
			return info.Mode().IsRegular() && info.Size() <= maxBytes(rel)
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
