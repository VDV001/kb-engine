// Package memorydir хранит двухуровневую память файлами в папке человека.
package memorydir

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	pendingDir = "pending"
	indexFile  = "INDEX.md"
)

// Служба эмбеддингов памяти по умолчанию — локальная Ollama с bge-m3 (ADR-0008): один
// адрес для терминала, MCP и окна. Без нее поиск идет по тексту.
const (
	EmbedURL   = "http://127.0.0.1:11434/api/embeddings"
	EmbedModel = "bge-m3"
)

// DefaultPath — папка памяти по умолчанию: рядом с настройками окна, чтобы окно
// и терминал видели одну память.
func DefaultPath(home string) string {
	return filepath.Join(home, "Library", "Application Support", "kb-desktop", "dvk-memory")
}

// Store — папка памяти: HOT.md, cold/ с INDEX.md, journal/ и pending/ (очередь одобрения).
type Store struct{ dir string }

// New points the store at dir.
func New(dir string) *Store { return &Store{dir: dir} }

const hotSkeleton = `# Горячая память

<!-- Читается в каждой сессии, поэтому короткая: kbengine memory check следит за лимитом.
     Всё, что нужно не каждый раз, живет в cold/ и находится по индексу. -->

## Кто я

<!-- Блок собирает kbengine memory who из портрета DVK (источник один), руками не править. -->
` + domain.PortraitStart + `
` + domain.PortraitEnd + `

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
	if err := os.WriteFile(idx, []byte(indexSkeleton), 0o600); err != nil {
		return err
	}
	return s.Seal()
}

// SaveHot replaces HOT.md: временный файл рядом и переименование, оборванная
// запись не оставляет полфайла (горячий слой читается каждой сессией).
func (s *Store) SaveHot(hot string) error {
	tmp, err := os.CreateTemp(s.dir, ".hot-*") // CreateTemp — 0600
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(hot); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, hotFile)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return s.writeSeal(domain.HotDigest(hot)) // своя запись системы запечатывается
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

	texts, err := s.ColdTexts()
	if err != nil {
		return snap, err
	}
	for _, t := range texts {
		snap.Notes = append(snap.Notes, t.Note)
	}
	return snap, nil
}

// ColdTexts reads every note of cold/ with its body and digest. Журнал, очередь и
// служебные файлы (INDEX.md, .vectors.json) сюда не входят.
func (s *Store) ColdTexts() ([]memory.NoteText, error) {
	cold := filepath.Join(s.dir, coldDir)
	entries, err := os.ReadDir(cold)
	if err != nil {
		return nil, err
	}
	var out []memory.NoteText
	for _, e := range entries {
		if e.IsDir() || e.Name() == indexFile || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(cold, e.Name()))
		if err != nil {
			return nil, err
		}
		n, body := parseNote(e.Name(), string(b))
		out = append(out, memory.NoteText{Note: n, Body: body})
	}
	return out, nil
}

const vectorsFile = ".vectors.json"

type vectorsJSON struct {
	Model   string                `json:"model"`
	Dims    int                   `json:"dims"`
	Built   string                `json:"built"`
	Vectors map[string]vectorJSON `json:"vectors"`
}

type vectorJSON struct {
	Digest string    `json:"digest"`
	V      []float32 `json:"v"`
}

// LoadVectors reads cold/.vectors.json; файла нет — memory.ErrNoVectors, битый
// файл — обычная ошибка (не путать «не снят» с «сломан»).
func (s *Store) LoadVectors() (memory.VectorIndex, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, coldDir, vectorsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return memory.VectorIndex{}, memory.ErrNoVectors
	}
	if err != nil {
		return memory.VectorIndex{}, err
	}
	var in vectorsJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return memory.VectorIndex{}, fmt.Errorf("%s: %w", vectorsFile, err)
	}
	out := memory.VectorIndex{Model: in.Model, Dims: in.Dims, Built: in.Built, Entries: make(map[string]memory.VectorEntry, len(in.Vectors))}
	for name, v := range in.Vectors {
		out.Entries[name] = memory.VectorEntry{Digest: v.Digest, Vector: v.V}
	}
	return out, nil
}

// SaveVectors writes the index atomically: временный файл рядом и переименование.
func (s *Store) SaveVectors(v memory.VectorIndex) error {
	out := vectorsJSON{Model: v.Model, Dims: v.Dims, Built: v.Built, Vectors: make(map[string]vectorJSON, len(v.Entries))}
	for name, e := range v.Entries {
		out.Vectors[name] = vectorJSON{Digest: e.Digest, V: e.Vector}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.dir, coldDir)
	tmp, err := os.CreateTemp(dir, ".vectors-*.tmp") // CreateTemp — 0600
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, vectorsFile)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// parseNote читает шапку между двумя «---» (name, topic, origin, description) и
// тело после нее. Нет шапки или имени — имя берется из имени файла.
func parseNote(file, text string) (n domain.ColdNote, body string) {
	n.File = file
	sum := sha256.Sum256([]byte(text))
	n.Digest = hex.EncodeToString(sum[:])
	lines := strings.Split(text, "\n")
	rest := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i, l := range lines[1:] {
			if strings.TrimSpace(l) == "---" {
				rest = i + 2
				break
			}
			k, v, ok := strings.Cut(l, ":")
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			switch strings.TrimSpace(k) {
			case "name":
				n.Name = v
			case "topic":
				n.Topic = v
			case "origin":
				n.Origin = domain.Origin(v)
			case "description":
				n.Description = v
			}
		}
	}
	if n.Name == "" {
		n.Name = strings.TrimSuffix(file, ".md")
	}
	return n, strings.TrimSpace(strings.Join(lines[rest:], "\n"))
}

// writeNote creates dir/n.File with the front matter and body; существующий файл не затирает.
func writeNote(dir string, n domain.ColdNote, body string) error {
	f, err := os.OpenFile(filepath.Join(dir, n.File), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %s", domain.ErrNoteExists, n.File)
	}
	if err != nil {
		return err
	}
	origin := n.Origin
	if origin == "" {
		origin = domain.OriginHuman
	}
	_, werr := fmt.Fprintf(f, "---\nname: %s\ntopic: %s\norigin: %s\ndescription: %s\n---\n\n%s\n", n.Name, n.Topic, origin, n.Description, strings.TrimSpace(body))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

func (s *Store) appendIndexLine(n domain.ColdNote) error {
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
	_, werr := fi.WriteString(line)
	if cerr := fi.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// Add writes the note and its index line.
// ponytail: две записи подряд без транзакции. Сбой между ними оставляет запись
// без строки в индексе, и `check` назовет ее not-indexed; путь вверх — писать
// во временный файл и переименовывать, если такое случится на деле.
func (s *Store) Add(n domain.ColdNote, body string) error {
	if err := writeNote(filepath.Join(s.dir, coldDir), n, body); err != nil {
		return err
	}
	return s.appendIndexLine(n)
}

// AddPending puts an agent's proposal in pending/: туда не заглядывают ни Load,
// ни индекс, ни перечень тем. Имя, занятое в холодном слое, не принимается.
func (s *Store) AddPending(n domain.ColdNote, body string) error {
	if _, err := os.Stat(filepath.Join(s.dir, coldDir, n.File)); err == nil {
		return fmt.Errorf("%w: %s", domain.ErrNoteExists, n.File)
	}
	dir := filepath.Join(s.dir, pendingDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeNote(dir, n, body)
}

// Pending lists the queue by file name; папки очереди еще нет — очередь пуста.
func (s *Store) Pending() ([]memory.PendingNote, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, pendingDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []memory.PendingNote
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, pendingDir, e.Name()))
		if err != nil {
			return nil, err
		}
		n, body := parseNote(e.Name(), string(b))
		p := memory.PendingNote{Note: n, Body: body}
		// ponytail: срок ожидания — время файла; копирование его сбросит и предложение
		// «помолодеет». Путь вверх: дата в шапке записи при Propose.
		if info, err := e.Info(); err == nil {
			p.Since = info.ModTime()
		}
		out = append(out, p)
	}
	return out, nil
}

// pendingPath validates name on the boundary: имя из флага не должно вести
// за пределы pending/.
func (s *Store) pendingPath(name string) (string, error) {
	if !domain.ValidNoteName(name) {
		return "", fmt.Errorf("%w: %q", domain.ErrNoSuchPending, name)
	}
	return filepath.Join(s.dir, pendingDir, name+".md"), nil
}

// ReadPending returns one queued note.
func (s *Store) ReadPending(name string) (memory.PendingNote, error) {
	path, err := s.pendingPath(name)
	if err != nil {
		return memory.PendingNote{}, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return memory.PendingNote{}, fmt.Errorf("%w: %q", domain.ErrNoSuchPending, name)
	}
	if err != nil {
		return memory.PendingNote{}, err
	}
	n, body := parseNote(name+".md", string(b))
	return memory.PendingNote{Note: n, Body: body}, nil
}

// moveNoClobber moves src to dst without overwriting: жесткая ссылка отказывает
// атомарно, если dst есть, а переименование молча затерло бы его. Где жестких
// ссылок нет (exFAT, SMB, облачные папки), переносит копированием.
func moveNoClobber(src, dst string) error {
	err := os.Link(src, dst)
	switch {
	case err == nil:
		return os.Remove(src)
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("%w: %s", domain.ErrNoteExists, filepath.Base(dst))
	}
	return moveByCopy(src, dst)
}

// moveByCopy creates dst exclusively, copies src into it and removes src.
func moveByCopy(src, dst string) error {
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %s", domain.ErrNoteExists, filepath.Base(dst))
	}
	if err != nil {
		return err
	}
	_, werr := f.Write(body)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(dst) // недописанная копия хуже ее отсутствия
		return werr
	}
	return os.Remove(src)
}

// PromotePending moves a queued note to cold/ and adds its index line. Если строку
// индекса записать не вышло, запись возвращается в очередь: ни «пол-готово», ни
// потерянной записи.
func (s *Store) PromotePending(name string) error {
	p, err := s.ReadPending(name)
	if err != nil {
		return err
	}
	src, _ := s.pendingPath(name)
	dst := filepath.Join(s.dir, coldDir, p.Note.File)
	if err := moveNoClobber(src, dst); err != nil {
		return err
	}
	if err := s.appendIndexLine(p.Note); err != nil {
		if rerr := moveNoClobber(dst, src); rerr != nil {
			return fmt.Errorf("индекс не записан (%w), и запись не вернулась в очередь: %w", err, rerr)
		}
		return fmt.Errorf("индекс не записан, запись осталась в очереди: %w", err)
	}
	return nil
}

// ArchivePending moves a queued note to journal/<as>: решение человека остается
// в журнале, ничего не удаляется. Имя занято (то же имя в тот же день) — к нему
// добавляется номер.
func (s *Store) ArchivePending(name, as string) error {
	src, err := s.pendingPath(name)
	if err != nil {
		return err
	}
	if filepath.Base(as) != as || !strings.HasSuffix(as, ".md") {
		return fmt.Errorf("%w: имя в журнале %q", domain.ErrInvalidNote, as)
	}
	stem := strings.TrimSuffix(as, ".md")
	for i := 1; i <= 99; i++ {
		target := as
		if i > 1 {
			target = fmt.Sprintf("%s-%d.md", stem, i)
		}
		err := moveNoClobber(src, filepath.Join(s.dir, journalDir, target))
		if !errors.Is(err, domain.ErrNoteExists) {
			return err
		}
	}
	return fmt.Errorf("%w: в журнале слишком много записей с именем %s", domain.ErrNoteExists, as)
}

// JournalNames lists journal/ by name; журнал только дописывается, поэтому порядок
// имен с датой в начале — порядок времени.
func (s *Store) JournalNames() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, journalDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && e.Name() != decisionsFile {
			out = append(out, e.Name())
		}
	}
	return out, nil // ReadDir уже отдает по имени
}

const limitsFile = "memory.json"

type limitsJSON struct {
	HotLimit   int `json:"hotLimit,omitempty"`
	IndexLimit int `json:"indexLimit,omitempty"`
}

// Limits reads the ceilings set for this memory; файла нет — нули (умолчания).
// Битый файл — ошибка: молчаливое умолчание сделало бы потолок человека невидимым.
func (s *Store) Limits() (memory.Limits, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, limitsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return memory.Limits{}, nil
	}
	if err != nil {
		return memory.Limits{}, err
	}
	var in limitsJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return memory.Limits{}, fmt.Errorf("%s: %w", limitsFile, err)
	}
	if err := domain.ValidateLimits(in.HotLimit, in.IndexLimit); err != nil {
		return memory.Limits{}, fmt.Errorf("%s: %w", limitsFile, err)
	}
	return memory.Limits{Hot: in.HotLimit, Index: in.IndexLimit}, nil
}

// SaveLimits stores the ceilings atomically; негодное значение прежний файл не трогает.
func (s *Store) SaveLimits(l memory.Limits) error {
	if err := domain.ValidateLimits(l.Hot, l.Index); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(limitsJSON{HotLimit: l.Hot, IndexLimit: l.Index}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".memory-*.tmp") // CreateTemp — 0600
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, limitsFile)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

const (
	sealFile      = ".seal"
	decisionsFile = "decisions.md"
)

// writeSeal stores the digest atomically, 0600.
func (s *Store) writeSeal(digest string) error {
	tmp, err := os.CreateTemp(s.dir, ".seal-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(digest + "\n"); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, sealFile)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Seal accepts the current HOT.md as is: запоминает его отпечаток.
// ponytail: печать — сигнал, а не замок. Тот, кто может править HOT.md, может
// править и .seal; она ловит правку мимо системы (агента с доступом к файлам,
// забытую ручную), но не остановит намеренную подделку обоих файлов. Путь вверх:
// печать в Связке ключей или подпись ключом из Secure Enclave.
func (s *Store) Seal() error {
	b, err := os.ReadFile(filepath.Join(s.dir, hotFile))
	if err != nil {
		return fmt.Errorf("горячий слой: %w", err)
	}
	return s.writeSeal(domain.HotDigest(string(b)))
}

// SealedDigest returns the accepted digest; печати нет — "" (память без печати не нарушение).
func (s *Store) SealedDigest() (string, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, sealFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

// AppendDecision appends one line to journal/decisions.md; файл только дописывается.
func (s *Store) AppendDecision(line string) error {
	line = strings.Join(strings.Fields(line), " ") // одна строка, без управляющих
	if err := os.MkdirAll(filepath.Join(s.dir, journalDir), 0o700); err != nil {
		return err
	}
	path := filepath.Join(s.dir, journalDir, decisionsFile)
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	text := "- " + line + "\n"
	if errors.Is(statErr, fs.ErrNotExist) {
		text = "# Журнал решений\n\nТолько дописывается: что человек одобрил, отклонил и подтвердил.\n\n" + text
	}
	_, werr := f.WriteString(text)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// Decisions returns the last n decisions, oldest first; файла нет — пусто.
func (s *Store) Decisions(n int) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, journalDir, decisionsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all []string
	for l := range strings.SplitSeq(string(b), "\n") {
		if rest, ok := strings.CutPrefix(l, "- "); ok {
			all = append(all, rest)
		}
	}
	return all[max(0, len(all)-n):], nil
}
