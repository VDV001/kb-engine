// Package tasklist is the inbound adapter that parses a task list — either the
// plaintext form pasted from the task tracker or a structured JSON export —
// into taskaudit.Task values for the ADR-015 audit.
package tasklist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/daniil/kb-engine/internal/usecase/taskaudit"
)

var (
	// reLine matches "#N [status] subject", tolerating a trailing dot after the
	// number and leading whitespace.
	reLine = regexp.MustCompile(`^\s*#(\d+)\.?\s*\[([a-zA-Z_]+)\]\s*(.+?)\s*$`)
	// reHabr extracts the article id from a "habr <id>" marker anywhere in the
	// subject, case-insensitively.
	reHabr = regexp.MustCompile(`(?i)\bhabr\s+(\d+)\b`)
)

// ParsePlain parses the plaintext task list, one task per matching line. Lines
// that do not match the "#N [status] subject" shape are ignored.
func ParsePlain(text string) ([]taskaudit.Task, error) {
	var tasks []taskaudit.Task
	for line := range strings.SplitSeq(text, "\n") {
		m := reLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		tasks = append(tasks, taskaudit.Task{
			ID:     m[1],
			Status: m[2],
			HabrID: habrID(m[3]),
		})
	}
	return tasks, nil
}

type jsonTask struct {
	ID          json.RawMessage `json:"id"`
	TaskID      json.RawMessage `json:"task_id"`
	Status      string          `json:"status"`
	Subject     string          `json:"subject"`
	Description string          `json:"description"`
}

// ParseJSON parses a structured task list: either a bare array of task objects
// or an object with a "tasks" array.
func ParseJSON(text string) ([]taskaudit.Task, error) {
	trimmed := bytes.TrimSpace([]byte(text))
	var raw []jsonTask
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var wrapper struct {
			Tasks []jsonTask `json:"tasks"`
		}
		if err := json.Unmarshal(trimmed, &wrapper); err != nil {
			return nil, fmt.Errorf("decode task object: %w", err)
		}
		raw = wrapper.Tasks
	} else {
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			return nil, fmt.Errorf("decode task array: %w", err)
		}
	}

	tasks := make([]taskaudit.Task, 0, len(raw))
	for _, t := range raw {
		subject := t.Subject
		if subject == "" {
			subject = t.Description
		}
		tasks = append(tasks, taskaudit.Task{
			ID:     scalarString(t.ID, t.TaskID),
			Status: t.Status,
			HabrID: habrID(subject),
		})
	}
	return tasks, nil
}

// habrID returns the article id referenced by a "habr <id>" marker, or "".
func habrID(subject string) string {
	if m := reHabr.FindStringSubmatch(subject); m != nil {
		return m[1]
	}
	return ""
}

// scalarString renders the first present JSON scalar (number or string) as a
// plain string, falling back to "?" when neither id field is set.
func scalarString(candidates ...json.RawMessage) string {
	for _, c := range candidates {
		s := strings.TrimSpace(string(c))
		if s == "" || s == "null" {
			continue
		}
		return strings.Trim(s, `"`)
	}
	return "?"
}
