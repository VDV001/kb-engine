// Package financevocab stores the words a person types for accounts and places
// next to the ledger they describe.
//
// The file is meant to be read and edited by hand, and it is the only copy:
// the terminal reads it, and so does the assistant in the chat. That is the
// whole point — two vocabularies would file the same shop under two categories
// within a month.
package financevocab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// FileName is the vocabulary's name next to the ledger, like the sync state.
const FileName = "finance-aliases.json"

// PathNextTo returns where the vocabulary lives for a given ledger.
func PathNextTo(ledgerPath string) string {
	return filepath.Join(filepath.Dir(ledgerPath), FileName)
}

// file is the shape on disk: keys as the owner wrote them, so the file stays
// readable. Normalising happens on the way in, not in the file.
type file struct {
	Accounts map[string]string    `json:"accounts"`
	Places   map[string]placeRule `json:"places"`
}

type placeRule struct {
	Category    string `json:"category"`
	Subcategory string `json:"subcategory,omitempty"`
	Place       string `json:"place,omitempty"`
}

// Load reads the vocabulary. A missing file is not a failure — it is a state
// the caller has to name, so the error wraps fs.ErrNotExist and the vocabulary
// comes back empty rather than nil.
//
// Keys are normalised on the way in, so «Пятерочка» and «Пятёрочка» become one
// word. When both spell the same rule that is exactly the point of the file and
// they collapse silently. When they spell DIFFERENT rules, the word is dropped
// and named: picking one would keep the coin flip, merely tossed once at load
// instead of on every run.
func Load(path string) (finance.Vocabulary, error) {
	v := finance.Vocabulary{
		Accounts: map[string]string{},
		Places:   map[string]finance.PlaceRule{},
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return v, err
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return v, fmt.Errorf("%s: %w", path, err)
	}

	accounts, accountConflicts := collapse(f.Accounts, func(a string) string { return a })
	places, placeConflicts := collapse(f.Places, func(r placeRule) string {
		return fmt.Sprintf("%s / %s → %s", r.Category, r.Subcategory, r.Place)
	})
	v.Accounts = accounts
	for word, rule := range places {
		v.Places[word] = finance.PlaceRule{
			Category:    rule.Category,
			Subcategory: rule.Subcategory,
			Place:       rule.Place,
		}
	}

	if lines := append(accountConflicts, placeConflicts...); len(lines) > 0 {
		return v, fmt.Errorf("%s: %w:\n  %s", path, finance.ErrVocabularyConflict, strings.Join(lines, "\n  "))
	}
	return v, nil
}

// collapse normalises keys and returns the surviving entries plus one line per
// conflict, sorted so the message does not reshuffle between runs — the defect
// being fixed here is precisely an answer that changes run to run.
func collapse[T comparable](in map[string]T, describe func(T) string) (map[string]T, []string) {
	type group struct {
		keys  []string
		rules []T
	}
	groups := map[string]*group{}
	for key, rule := range in {
		n := finance.NormalizeWord(key)
		g, ok := groups[n]
		if !ok {
			g = &group{}
			groups[n] = g
		}
		g.keys = append(g.keys, key)
		if !slices.Contains(g.rules, rule) {
			g.rules = append(g.rules, rule)
		}
	}

	out := make(map[string]T, len(groups))
	var conflicts []string
	for n, g := range groups {
		if len(g.rules) == 1 {
			out[n] = g.rules[0]
			continue
		}
		slices.Sort(g.keys)
		described := make([]string, 0, len(g.rules))
		for _, r := range g.rules {
			described = append(described, describe(r))
		}
		slices.Sort(described)
		conflicts = append(conflicts, fmt.Sprintf("%q: %s — правила разные: %s",
			n, strings.Join(g.keys, ", "), strings.Join(described, " | ")))
	}
	slices.Sort(conflicts)
	return out, conflicts
}

// RememberAccount adds one word for an account, keeping everything else.
func RememberAccount(path, word, account string) error {
	return update(path, func(f *file) {
		f.Accounts[word] = account
	})
}

// RememberPlace adds one word for a place, keeping everything else.
func RememberPlace(path, word string, rule finance.PlaceRule) error {
	return update(path, func(f *file) {
		f.Places[word] = placeRule{Category: rule.Category, Subcategory: rule.Subcategory, Place: rule.Place}
	})
}

// update rewrites the file with one change applied. The whole file is read
// first: a word learned in the terminal must not drop the words added by hand.
func update(path string, apply func(*file)) error {
	f := file{Accounts: map[string]string{}, Places: map[string]placeRule{}}
	switch raw, err := os.ReadFile(path); {
	case err == nil:
		if err := json.Unmarshal(raw, &f); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if f.Accounts == nil {
			f.Accounts = map[string]string{}
		}
		if f.Places == nil {
			f.Places = map[string]placeRule{}
		}
	case os.IsNotExist(err):
		// The first word creates the vocabulary.
	default:
		return err
	}

	apply(&f)

	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}
