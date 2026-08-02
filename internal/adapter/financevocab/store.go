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
	for word, account := range f.Accounts {
		v.Accounts[finance.NormalizeWord(word)] = account
	}
	for word, rule := range f.Places {
		v.Places[finance.NormalizeWord(word)] = finance.PlaceRule{
			Category:    rule.Category,
			Subcategory: rule.Subcategory,
			Place:       rule.Place,
		}
	}
	return v, nil
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
