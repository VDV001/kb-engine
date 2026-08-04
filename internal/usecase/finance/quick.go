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
// assistant in the chat. Two copies would file the same shop under
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
// The rule itself lives in the domain: the Счета sheet asks the same question
// when it has to refuse a second row for an account it already holds under
// another spelling, and two copies of one rule are how one surface starts
// folding «ё» while the other stops.
func NormalizeWord(w string) string { return domain.FoldName(w) }

// maxNameWords is how many neighbouring words may form one name. Three covers
// what the vocabulary holds («Италиан Пицца», «Заморозка → Вклад») without
// turning the scan into a search over the whole line.
const maxNameWords = 3

// ParseQuick reads one line typed the way a person speaks: an amount and a few
// words. Word order is free — the words name different things, so their
// position carries nothing.
//
// Names of several words are matched before single ones, longest first: «яндекс
// такси» is a place of its own, and reading it as «такси» would drop where the
// ride was bought.
func ParseQuick(line string, v Vocabulary) (QuickEntry, error) {
	var out QuickEntry
	out.Params.Kind = domain.KindExpense

	// Заметка отделяется тире или двоеточием с пробелами вокруг и дальше не
	// разбирается: это свободный текст, и слова в нём не обязаны быть в словаре.
	//
	// Пробелы обязательны, и это не педантизм: «Альфа-Банк» и «Т-Банк» несут
	// дефис внутри имени, а деление по голому дефису разрезало бы половину
	// словаря счетов.
	line, note := splitNote(line)
	out.Params.Description = note

	words := strings.Fields(line)
	var seenAmount bool
	for i := 0; i < len(words); {
		// The amount is recognised by being a number, not by position. A second
		// number is not silently dropped: it is reported like any other word the
		// engine cannot place.
		if !seenAmount {
			if m, err := domain.ParseMoney(words[i]); err == nil {
				out.Params.Amount, seenAmount = m, true
				i++
				continue
			}
		}

		matched := false
		for n := min(maxNameWords, len(words)-i); n >= 1 && !matched; n-- {
			key := NormalizeWord(strings.Join(words[i:i+n], ""))
			if key == "" {
				continue
			}
			switch {
			case v.Accounts[key] != "":
				out.Params.Account = v.Accounts[key]
				matched = true
			default:
				if rule, ok := v.Places[key]; ok {
					out.Params.Category = rule.Category
					out.Params.Subcategory = rule.Subcategory
					out.Params.Place = rule.Place
					matched = true
				}
			}
			if matched {
				i += n
			}
		}
		if !matched {
			out.Unknown = append(out.Unknown, words[i])
			i++
		}
	}

	if !seenAmount {
		return QuickEntry{}, fmt.Errorf("%w: %q", ErrNoAmount, line)
	}
	return out, nil
}

// splitNote отрезает от строки хвост-заметку. Возвращает то, что осталось для
// разбора, и саму заметку.
func splitNote(line string) (rest, note string) {
	// Порядок важен: длинное тире ищется раньше короткого, иначе строка с «—»
	// осталась бы нетронутой.
	for _, sep := range []string{" — ", " – ", " - ", " : "} {
		if head, tail, found := strings.Cut(line, sep); found {
			return head, strings.TrimSpace(tail)
		}
	}
	return line, ""
}
