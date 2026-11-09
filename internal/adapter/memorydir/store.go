// Package memorydir хранит двухуровневую память файлами в папке человека.
package memorydir

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

const (
	hotFile    = "HOT.md"
	coldDir    = "cold"
	journalDir = "journal"
	indexFile  = "INDEX.md"
)

// DefaultPath — папка памяти по умолчанию: рядом с настройками окна, чтобы окно
// и терминал видели одну память.
func DefaultPath(home string) string {
	return filepath.Join(home, "Library", "Application Support", "kb-desktop", "dvk-memory")
}

// Store — папка памяти: HOT.md, cold/ с INDEX.md и journal/.
type Store struct{ dir string }

// New points the store at dir.
func New(dir string) *Store { return &Store{dir: dir} }

const hotSkeleton = `# Горячая память

<!-- Читается в каждой сессии, поэтому короткая: kbengine memory check следит за лимитом.
     Всё, что нужно не каждый раз, живет в cold/ и находится по индексу. -->

## Кто я

-

## Правила работы

-

## Сейчас в работе

-

## Холодная память

Целиком не загружается: факты лежат в cold/, по файлу на факт.
Тема не про правила выше: сначала поищи в холодной памяти (kbengine memory find), потом отвечай.

Темы холодной памяти (собирает kbengine memory topics):

` + domain.TopicsStart + `
` + domain.TopicsEnd + `
`

const indexSkeleton = `# Индекс холодной памяти

<!-- Одна строка на запись: - [имя](файл.md) — крючок до 150 знаков.
     Добавляет человек руками или kbengine memory add. -->
`

// Init заводит скелет; существующую память не трогает.
func (s *Store) Init() error {
	hot := filepath.Join(s.dir, hotFile)
	idx := filepath.Join(s.dir, coldDir, indexFile)
	for _, p := range []string{hot, idx} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%w: %s", domain.ErrMemoryExists, p)
		}
	}
	for _, d := range []string{coldDir, journalDir} {
		if err := os.MkdirAll(filepath.Join(s.dir, d), 0o700); err != nil {
			return err
		}
	}
	if err := os.WriteFile(hot, []byte(hotSkeleton), 0o600); err != nil {
		return err
	}
	return os.WriteFile(idx, []byte(indexSkeleton), 0o600)
}

var indexLine = regexp.MustCompile(`^- \[[^\]]*\]\(([^)]+)\)(?:\s+[—-]\s+(.*))?$`)

// Load reads the layer files.
func (s *Store) Load() (memory.Snapshot, error) {
	var snap memory.Snapshot
	hot, err := os.ReadFile(filepath.Join(s.dir, hotFile))
	if err != nil {
		return snap, fmt.Errorf("горячий слой: %w", err)
	}
	snap.Hot = string(hot)

	cold := filepath.Join(s.dir, coldDir)
	idx, err := os.ReadFile(filepath.Join(cold, indexFile))
	if err != nil {
		return snap, fmt.Errorf("индекс: %w", err)
	}
	snap.IndexRunes = utf8.RuneCount(idx)
	sc := bufio.NewScanner(strings.NewReader(string(idx)))
	for sc.Scan() {
		if m := indexLine.FindStringSubmatch(strings.TrimSpace(sc.Text())); m != nil {
			snap.Index = append(snap.Index, domain.IndexEntry{File: m[1], Hook: strings.TrimSpace(m[2])})
		}
	}

	entries, err := os.ReadDir(cold)
	if err != nil {
		return snap, err
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == indexFile || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(cold, e.Name()))
		if err != nil {
			return snap, err
		}
		name, topic, desc := frontmatter(string(b))
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".md")
		}
		snap.Notes = append(snap.Notes, domain.ColdNote{File: e.Name(), Name: name, Topic: topic, Description: desc})
	}
	return snap, nil
}

// frontmatter читает name, topic и description из шапки между двумя «---».
func frontmatter(text string) (name, topic, desc string) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", ""
	}
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "---" {
			break
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "name":
			name = strings.TrimSpace(v)
		case "topic":
			topic = strings.TrimSpace(v)
		case "description":
			desc = strings.TrimSpace(v)
		}
	}
	return name, topic, desc
}

// Add writes the note and its index line.
// ponytail: две записи подряд без транзакции. Сбой между ними оставляет запись
// без строки в индексе, и `check` назовет ее not-indexed; путь вверх — писать
// во временный файл и переименовывать, если такое случится на деле.
func (s *Store) Add(n domain.ColdNote, body string) error {
	path := filepath.Join(s.dir, coldDir, n.File)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %s", domain.ErrNoteExists, n.File)
	}
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintf(f, "---\nname: %s\ntopic: %s\ndescription: %s\n---\n\n%s\n", n.Name, n.Topic, n.Description, strings.TrimSpace(body))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}

	idx := filepath.Join(s.dir, coldDir, indexFile)
	cur, err := os.ReadFile(idx)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("- [%s](%s) — %s\n", n.Name, n.File, n.Description)
	if len(cur) > 0 && cur[len(cur)-1] != '\n' {
		line = "\n" + line
	}
	fi, err := os.OpenFile(idx, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, werr = fi.WriteString(line)
	if cerr := fi.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
