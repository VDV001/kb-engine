package filesearch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// SnapshotVersion — версия формата снимка. Снимок другой версии считается
// отсутствующим: индекс соберется заново, а не прочитается неверно.
const SnapshotVersion = 1

// ErrBadSnapshot is returned when a snapshot does not describe a valid index.
var ErrBadSnapshot = errors.New("bad index snapshot")

// rules — отпечаток правил, по которым из текста получаются основы. Снимок
// хранит основы, а не текст: собранный другими правилами, он не совпадет с
// основами запроса, и слова молча перестанут находиться (ревью #374). Поэтому
// формат версионируется отдельно (SnapshotVersion), а правила — отпечатком,
// который меняется сам, стоит поправить окончания или пороги.
var rules = func() string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%d|%d|%d|%d",
		strings.Join(russianEndings, ","), minStemRunes, minTokenRunes, idMixedRunes, idMaxRunes))
	return hex.EncodeToString(sum[:8])
}()

// DocRecord is one indexed file in a snapshot: enough to decide, without
// reading the file, whether it changed since. Время — секунды и наносекунды
// отдельно: UnixNano переполняется для дат вне 1678–2262 (файлы из архивов).
type DocRecord struct {
	Path    string
	Size    int64
	ModSec  int64
	ModNsec int32
}

// Snapshot is the index in a form a store can write and read back. Postings[i]
// belongs to Terms[i]; Posting.Doc indexes Docs.
type Snapshot struct {
	Version  int
	Rules    string // отпечаток правил разбора, см. rules
	BuiltSec int64  // когда собран индекс: файлы моложе перечитываются
	Docs     []DocRecord
	Terms    []string
	Postings [][]Posting
}

// Snapshot returns the index as plain data. Списки вхождений не копируются:
// индекс после сборки не меняется.
func (ix *Index) Snapshot() Snapshot {
	s := Snapshot{
		Version:  SnapshotVersion,
		Rules:    rules,
		BuiltSec: ix.builtAt.Unix(),
		Docs:     make([]DocRecord, len(ix.docs)),
		Terms:    make([]string, 0, len(ix.postings)),
		Postings: make([][]Posting, 0, len(ix.postings)),
	}
	for i, d := range ix.docs {
		m := d.ModTime()
		s.Docs[i] = DocRecord{Path: d.Path(), Size: d.Size(), ModSec: m.Unix(), ModNsec: int32(m.Nanosecond())}
	}
	for t, p := range ix.postings {
		s.Terms = append(s.Terms, t)
		s.Postings = append(s.Postings, p)
	}
	return s
}

// IndexFromSnapshot rebuilds an index from a snapshot and checks it on the way:
// снимок читается с диска, то есть это внешние данные, и ссылка на
// несуществующий файл уронила бы поиск паникой.
func IndexFromSnapshot(s Snapshot) (*Index, error) {
	if s.Version != SnapshotVersion {
		return nil, fmt.Errorf("%w: version %d, want %d", ErrBadSnapshot, s.Version, SnapshotVersion)
	}
	if s.Rules != rules {
		return nil, fmt.Errorf("%w: built with other word rules", ErrBadSnapshot)
	}
	if len(s.Terms) != len(s.Postings) {
		return nil, fmt.Errorf("%w: %d terms, %d posting lists", ErrBadSnapshot, len(s.Terms), len(s.Postings))
	}
	ix := NewIndex()
	ix.builtAt = time.Unix(s.BuiltSec, 0)
	for _, r := range s.Docs {
		d, err := domain.NewFileDoc(r.Path, r.Size, time.Unix(r.ModSec, int64(r.ModNsec)))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrBadSnapshot, err)
		}
		ix.addDoc(d)
	}
	n := int32(len(ix.docs))
	for i, t := range s.Terms {
		for _, p := range s.Postings[i] {
			if p.Doc < 0 || p.Doc >= n {
				return nil, fmt.Errorf("%w: term %q points at file %d of %d", ErrBadSnapshot, t, p.Doc, n)
			}
		}
		ix.postings[t] = s.Postings[i]
	}
	return ix, nil
}
