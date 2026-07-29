// Package financejsonl stores the ledger as newline-delimited JSON: one
// transaction per line, in a file meant to be read by a person and diffed by
// git. It is the anti-corruption layer on the way back in — a line that cannot
// be decoded into a valid domain transaction stops the load rather than being
// skipped.
package financejsonl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// ErrMalformedLine is returned when a line is not a decodable record.
var ErrMalformedLine = errors.New("malformed ledger line")

// maxLineBytes bounds a single line. Real lines are a few hundred bytes; the
// limit exists so an unbounded read cannot be mistaken for a valid one, and it
// is reported as an error rather than truncating the ledger silently.
const maxLineBytes = 1 << 20

// line is the wire shape of one record.
//
// The amount is a string, not a number: JSON numbers are floats to most
// readers, and the whole point of holding money in kopecks is that no float
// ever touches it. "202.45" is also what the amount looks like when spoken,
// which matters for a file that gets read by eye.
//
// The date is a day, not a moment — the ledger records what was spent on a
// day, and pretending to a precision it does not have would invite time-zone
// arithmetic that has nothing to reconcile.
type line struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Date        string `json:"date"`
	Amount      string `json:"amount"`
	Category    string `json:"category,omitempty"`
	Subcategory string `json:"subcategory,omitempty"`
	Place       string `json:"place,omitempty"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
	Account     string `json:"account,omitempty"`
	Rev         int    `json:"rev"`
	UpdatedAt   string `json:"updated_at"`
}

// Load reads every record from path, validating each against the given clock.
//
// A missing file is an error, not an empty ledger: reporting zero transactions
// because the file was not where it was expected is how a balance silently
// becomes wrong.
func Load(path string, now func() time.Time) ([]finance.Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	var out []finance.Record
	seen := make(map[string]struct{})
	for n := 1; sc.Scan(); n++ {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		rec, err := decode(raw, now)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", filepath.Base(path), n, err)
		}
		id := rec.Transaction().ID()
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("%s line %d: %w: %q", filepath.Base(path), n, finance.ErrDuplicateID, id)
		}
		seen[id] = struct{}{}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read ledger: %w", err)
	}
	return out, nil
}

// decode turns one line into a validated record. Every failure carries the
// error of the layer that rejected it, so the caller can tell a typo in the
// file from a violated invariant.
func decode(raw []byte, now func() time.Time) (finance.Record, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	// A field the struct does not know is a typo in a hand-edited file far more
	// often than it is a record from the future. Refusing beats importing a row
	// with its amount silently dropped.
	dec.DisallowUnknownFields()

	var l line
	if err := dec.Decode(&l); err != nil {
		return finance.Record{}, fmt.Errorf("%w: %w", ErrMalformedLine, err)
	}

	date, err := time.Parse(time.DateOnly, l.Date)
	if err != nil {
		return finance.Record{}, fmt.Errorf("%w: date %q: %w", ErrMalformedLine, l.Date, err)
	}
	updatedAt, err := time.Parse(time.RFC3339, l.UpdatedAt)
	if err != nil {
		return finance.Record{}, fmt.Errorf("%w: updated_at %q: %w", ErrMalformedLine, l.UpdatedAt, err)
	}
	amount, err := domain.ParseMoney(l.Amount)
	if err != nil {
		return finance.Record{}, err
	}

	// The clock belongs to the load, not to the record. Validating a row against
	// its own updated_at looks reproducible and is wrong: an entry made at 02:43
	// east of UTC is dated the 29th and stamped 21:43Z on the 28th, so it would be
	// a future entry forever and could never be read back. Time only ever makes a
	// date less future, so a real clock cannot reject what was accepted on write.
	tx, err := domain.NewTransaction(domain.TransactionParams{
		ID:          l.ID,
		Kind:        l.Kind,
		Date:        date,
		Amount:      amount,
		Category:    l.Category,
		Subcategory: l.Subcategory,
		Place:       l.Place,
		Description: l.Description,
		Source:      l.Source,
		Account:     l.Account,
		Now:         now,
	})
	if err != nil {
		return finance.Record{}, err
	}
	return finance.NewRecord(tx, l.Rev, updatedAt)
}

// Save writes every record to path, replacing what was there.
//
// Through a temp file in the same directory and an atomic rename: a crash
// halfway through must leave the previous ledger intact rather than a truncated
// one. Same directory because rename is only atomic within a filesystem.
func Save(path string, recs []finance.Record) error {
	return writeAtomically(path, func(w io.Writer) error {
		enc := json.NewEncoder(w)
		// The ledger holds descriptions with ampersands and angle brackets in them.
		// Go's default HTML escaping rewrites those as numeric escapes: correct
		// JSON, unreadable text. This file is read by eye.
		enc.SetEscapeHTML(false)
		for _, r := range recs {
			if err := enc.Encode(encodeLine(r)); err != nil {
				return fmt.Errorf("encode record %s: %w", r.Transaction().ID(), err)
			}
		}
		return nil
	})
}

// writeAtomically replaces path with whatever write produces, via a temp file
// in the same directory and a rename.
//
// Same directory because rename is only atomic within a filesystem, and sync
// before rename because otherwise the new name can become visible while its
// contents are still in flight.
func writeAtomically(path string, write func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Removing a name that was already renamed away is a no-op, so this is safe
	// on the success path too.
	defer func() { _ = os.Remove(tmpName) }()

	w := bufio.NewWriter(tmp)
	if err := write(w); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync %s: %w", filepath.Base(path), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

func encodeLine(r finance.Record) line {
	tx := r.Transaction()
	return line{
		ID:          tx.ID(),
		Kind:        tx.Kind(),
		Date:        tx.Date().Format(time.DateOnly),
		Amount:      tx.Amount().String(),
		Category:    tx.Category(),
		Subcategory: tx.Subcategory(),
		Place:       tx.Place(),
		Description: tx.Description(),
		Source:      tx.Source(),
		Account:     tx.Account(),
		Rev:         r.Rev(),
		UpdatedAt:   r.UpdatedAt().UTC().Format(time.RFC3339),
	}
}
