package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// VocabularySource is the words this screen understands, and the way it learns
// a new one. Declared here, in the consumer, like every other interface the
// screen needs.
//
// The vocabulary is re-read rather than cached: it is a file the owner edits by
// hand and the assistant in the chat writes to, so a copy held in memory would
// be stale by the time it matters.
type VocabularySource interface {
	Vocabulary() (finance.Vocabulary, error)
	Remember(word string, rule finance.PlaceRule) error
}

// WithVocabulary enables the one-line entry. Without it the key is absent —
// a line nobody can read is worse than no line.
func (m Model) WithVocabulary(v VocabularySource) Model {
	m.vocab = v
	return m
}

// quickForm is the one-line entry: what was typed, what the engine made of it,
// and what it could not place.
//
// A zero form means the line is not open; `typing` is the flag rather than an
// empty string, because an empty line is a legitimate state of an open form.
type quickForm struct {
	open    bool
	line    string
	parsed  *finance.QuickEntry
	err     string
	unknown []string
}

// OnQuickEntry reports whether the one-line entry is open.
func (m Model) OnQuickEntry() bool { return m.quick.open }

// openQuick starts the one-line entry, if there is a vocabulary to read it with.
func (m Model) openQuick() Model {
	if m.vocab == nil || m.ledger == nil {
		return m
	}
	m.quick = quickForm{open: true}
	m.finStatus = ""
	return m
}

// updateQuick handles one key on the one-line entry.
//
// Enter has two meanings in sequence, and that is the point: the first reads
// the line and shows what it understood, the second writes. A guess written
// straight to the file is one the person never had a chance to refuse.
func (m Model) updateQuick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.quick = quickForm{}
	case tea.KeyEnter:
		if m.quick.parsed == nil {
			return m.readQuickLine(), nil
		}
		return m.writeQuickLine(), nil
	case tea.KeyBackspace:
		// Editing the line drops the previous reading: what is shown must belong
		// to what is typed now.
		if r := []rune(m.quick.line); len(r) > 0 {
			m.quick.line = string(r[:len(r)-1])
		}
		m.quick.parsed, m.quick.err, m.quick.unknown = nil, "", nil
	case tea.KeyRunes, tea.KeySpace:
		m.quick.line += string(msg.Runes)
		m.quick.parsed, m.quick.err, m.quick.unknown = nil, "", nil
	}
	return m, nil
}

// readQuickLine parses the line and keeps the result on screen for approval.
func (m Model) readQuickLine() Model {
	voc, err := m.vocab.Vocabulary()
	if err != nil {
		// The vocabulary may simply not exist yet. Saying so beats reading the
		// line with an empty one and reporting every word as unknown.
		m.quick.err = fmt.Sprintf("словарь не прочитан: %v", err)
		return m
	}
	parsed, err := finance.ParseQuick(m.quick.line, voc)
	if err != nil {
		m.quick.err = err.Error()
		return m
	}
	m.quick.parsed, m.quick.unknown, m.quick.err = &parsed, parsed.Unknown, ""
	return m
}

// writeQuickLine records the entry, unless something is still undecided.
//
// An unknown word blocks the write on purpose: the engine may propose, but the
// decision about what a word means is the owner's. Writing «Прочее» over it
// would bury the question instead of asking it.
func (m Model) writeQuickLine() Model {
	if len(m.quick.unknown) > 0 {
		m.quick.err = fmt.Sprintf("не знаю слов: %s — назовите категорию или уберите их из строки",
			strings.Join(m.quick.unknown, ", "))
		return m
	}
	p := m.quick.parsed.Params
	fixed, err := m.ledger.Add(p)
	if err != nil {
		m.quick.err = fmt.Sprintf("не записано: %v", err)
		return m
	}

	m.quick = quickForm{}
	m.finStatus = fmt.Sprintf("записано: %s %s %s", kindName(p.Kind), p.Amount, or(p.Category, p.Source)) +
		correctionNote(fixed)
	m.workbookBehind = true
	m.summary, m.finErr = m.finances.Summary(nil)
	return m
}

func (m Model) renderQuick() string {
	var b strings.Builder
	b.WriteString(styleQuery.Render("строкой: "+m.quick.line) + "\n\n")

	// Пока не введено ни символа, экран разбирает пример вместо настоящей
	// строки: приглашение само по себе не говорит, писать ли сумму первой,
	// нужен ли счёт и как его называть. С первым символом разбор уступает
	// место настоящему — два разбора сразу читались бы как один.
	if m.quick.line == "" && m.quick.parsed == nil {
		b.WriteString(styleDim.Render("пример:  418р такси сбер") + "\n\n")
		for _, r := range [][2]string{
			{"сумма", "418р"},
			{"место", "такси"},
			{"счёт", "сбер · известные: " + m.accountHint()},
		} {
			b.WriteString(styleDim.Render(fmt.Sprintf("%-14s%s", r[0], r[1])) + "\n")
		}
	}

	if p := m.quick.parsed; p != nil {
		rows := [][2]string{
			{"сумма", p.Params.Amount.String()},
			{"категория", or(p.Params.Category, "—")},
			{"подкатегория", or(p.Params.Subcategory, "—")},
			{"место", or(p.Params.Place, "—")},
			{"счёт", or(p.Params.Account, "—")},
		}
		for _, r := range rows {
			b.WriteString(styleDim.Render(fmt.Sprintf("%-14s", r[0])) + r[1] + "\n")
		}
		if len(m.quick.unknown) > 0 {
			b.WriteString("\n" + styleTitle.Render("не знаю: "+strings.Join(m.quick.unknown, ", ")) + "\n")
		}
	}

	if m.quick.err != "" {
		b.WriteString("\n" + styleTitle.Render(m.quick.err) + "\n")
	}

	hint := hintQuickRead
	if m.quick.parsed != nil {
		hint = hintQuickWrite
	}
	return b.String() + "\n" + styleDim.Render(hint)
}

const (
	// Пример переехал наверх, в разбор по частям, и здесь не повторяется:
	// два примера на одном экране заставляют искать между ними разницу.
	hintQuickRead  = "Enter — разобрать · Esc — отмена"
	hintQuickWrite = "Enter — записать · правьте строку, чтобы разобрать заново · Esc — отмена"
	hintQuickKey   = "q — строкой"
)
