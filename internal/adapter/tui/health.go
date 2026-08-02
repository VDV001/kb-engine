package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/daniil/kb-engine/internal/usecase/audit"
)

// HealthSource is what the health screen needs: the whole summary in one call.
//
// One call rather than one per check on purpose — the composition of "health"
// is decided in the use case, so this screen cannot show a different set of
// checks than the dashboard does by forgetting one of them.
type HealthSource interface {
	Health() (audit.Health, error)
}

// WithHealth attaches the audit summary. Without it the screen is absent from
// the Tab cycle rather than present and empty.
func (m Model) WithHealth(h HealthSource) Model {
	m.health = h
	return m
}

// OnHealth reports whether the health screen is open.
func (m Model) OnHealth() bool { return m.onHealth }

// openHealth re-reads the summary every time.
//
// The audit answers a question about a catalog edited from this very terminal,
// so an answer taken once at startup is stale by the time anyone looks at it.
func (m Model) openHealth() (tea.Model, tea.Cmd) {
	m.onHealth = true
	m.healthSummary, m.healthErr = m.health.Health()
	return m, nil
}

func (m Model) updateHealth(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.onHealth = false
		return m, nil
	case tea.KeyTab:
		return m.nextScreen()
	}
	return m, nil
}

func (m Model) renderHealth() string {
	var b strings.Builder
	b.WriteString(styleQuery.Render("здоровье базы") + "\n\n")

	if m.healthErr != nil {
		b.WriteString(styleTitle.Render("сводка не построена") + "\n")
		b.WriteString(m.healthErr.Error() + "\n")
		return b.String() + "\n" + styleDim.Render(hintHealth)
	}

	h := m.healthSummary
	if h.Total() == 0 {
		b.WriteString(styleDim.Render("находок нет") + "\n\n")
	}
	writeFindings(&b, "устарели по признакам", h.Outdated)
	writeFindings(&b, "кандидаты в канон", h.Canonical)
	writeFindings(&b, "замещение", h.Supersession)
	writeDuplicates(&b, h.Duplicates)
	writeLinks(&b, h.Links)

	return b.String() + "\n" + styleDim.Render(hintHealth)
}

// writeFindings prints one section, or nothing at all when it is empty: a
// heading over an empty list reads as a problem with the screen.
func writeFindings(b *strings.Builder, title string, findings []audit.Finding) {
	if len(findings) == 0 {
		return
	}
	fmt.Fprintf(b, "%s %s\n", styleTitle.Render(title), styleDim.Render(fmt.Sprintf("(%d)", len(findings))))
	for _, f := range findings[:min(len(findings), healthRows)] {
		fmt.Fprintf(b, "  #%-6d %-40s %s\n", f.EntryID, trim(f.Title, 40), styleDim.Render(strings.Join(f.Reasons, ", ")))
	}
	if len(findings) > healthRows {
		b.WriteString(styleDim.Render(fmt.Sprintf("  … ещё %d\n", len(findings)-healthRows)))
	}
	b.WriteString("\n")
}

func writeDuplicates(b *strings.Builder, groups []audit.DuplicateGroup) {
	if len(groups) == 0 {
		return
	}
	fmt.Fprintf(b, "%s %s\n", styleTitle.Render("дубли"), styleDim.Render(fmt.Sprintf("(%d)", len(groups))))
	for _, g := range groups[:min(len(groups), healthRows)] {
		ids := make([]string, 0, len(g.EntryIDs))
		for _, id := range g.EntryIDs {
			ids = append(ids, fmt.Sprintf("#%d", id))
		}
		fmt.Fprintf(b, "  %-16s %-30s %s\n", g.Kind, trim(g.Key, 30), styleDim.Render(strings.Join(ids, " ")))
	}
	if len(groups) > healthRows {
		b.WriteString(styleDim.Render(fmt.Sprintf("  … ещё %d\n", len(groups)-healthRows)))
	}
	b.WriteString("\n")
}

// writeLinks prints the link scan, and it prints the unchecked count always —
// including zero. A tool that stays silent about what it did not check leaves a
// gap that looks exactly like verified cleanliness.
func writeLinks(b *strings.Builder, l audit.LinkHealth) {
	b.WriteString(styleTitle.Render("ссылки") + "\n")
	fmt.Fprintf(b, "  %d отвечают · %d переехали · %d исчезли · %d «не знаем» · %d не спрашивали\n",
		l.Alive, l.Moved, l.Gone, l.Undecidable, l.Unchecked)
	fmt.Fprintf(b, "  %s\n\n", styleDim.Render(fmt.Sprintf("из %d записей с адресом", l.WithURL)))
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

const (
	// healthRows caps each section so the screen stays one screen.
	healthRows = 6
	hintHealth = "Tab — следующий экран · Esc — назад · Ctrl+C — выход"
)
