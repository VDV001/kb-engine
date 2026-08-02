package finance

import (
	"errors"
	"fmt"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNoAmount is returned when a quick line carries no number to spend.
var ErrNoAmount = errors.New("в строке нет суммы")

// PlaceRule is what a known word says about where the money went.
type PlaceRule struct {
	Category    string
	Subcategory string
	Place       string
}

// Vocabulary maps the words a person types onto the values the ledger stores.
//
// One vocabulary for every surface: the terminal reads it, and so does the
// assistant in the chat. Two copies would file the same «Живика» under
// different categories inside a month, and the breakdown by category would
// quietly split in two.
//
// Keys are expected normalised (see NormalizeWord); loading is the adapter's
// job, and it normalises on the way in.
type Vocabulary struct {
	Accounts map[string]string
	Places   map[string]PlaceRule
}

// QuickEntry is a parsed line: what the engine understood, and what it did not.
//
// Unknown is not an error. The engine names the words it does not know and
// leaves the decision to the person — a guess written into the ledger is worse
// than a question asked once.
type QuickEntry struct {
	Params  AddParams
	Unknown []string
}

// NormalizeWord reduces a word to what two spellings of it have in common:
// case, «ё» and hyphens carry no meaning here. «Т-Банк», «т банк» and «тбанк»
// are one word; «Сбер» and «сбер» are one word.
func NormalizeWord(w string) string {
	w = strings.ToLower(strings.TrimSpace(w))
	w = strings.ReplaceAll(w, "ё", "е")
	w = strings.ReplaceAll(w, "-", "")
	w = strings.ReplaceAll(w, " ", "")
	return w
}

// ParseQuick reads one line typed the way a person speaks: an amount and a few
// words. Word order is free — the words name different things, so their
// position carries nothing.
func ParseQuick(line string, v Vocabulary) (QuickEntry, error) {
	var out QuickEntry
	out.Params.Kind = domain.KindExpense

	var seenAmount bool
	for _, word := range strings.Fields(line) {
		key := NormalizeWord(word)
		if key == "" {
			continue
		}

		// The amount is recognised by being a number, not by position. A second
		// number is not silently dropped: it is reported like any other word the
		// engine cannot place.
		if !seenAmount {
			if m, err := domain.ParseMoney(word); err == nil {
				out.Params.Amount, seenAmount = m, true
				continue
			}
		}

		if account, ok := v.Accounts[key]; ok {
			out.Params.Account = account
			continue
		}
		if rule, ok := v.Places[key]; ok {
			out.Params.Category = rule.Category
			out.Params.Subcategory = rule.Subcategory
			out.Params.Place = rule.Place
			continue
		}
		out.Unknown = append(out.Unknown, word)
	}

	if !seenAmount {
		return QuickEntry{}, fmt.Errorf("%w: %q", ErrNoAmount, line)
	}
	return out, nil
}
