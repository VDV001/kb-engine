package tasklist_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/tasklist"
)

func TestParsePlain(t *testing.T) {
	const in = `
some preamble that is not a task
#32 [completed] #40 habr 1024770 Entry-level hiring -73%
  #33. [pending] habr 1030928 Lexis article
#34 [in_progress] no habr id here
not a task line
#35 [completed] HABR 999 case-insensitive marker
`
	got, err := tasklist.ParsePlain(in)
	if err != nil {
		t.Fatalf("ParsePlain: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("parsed %d tasks, want 4:\n%+v", len(got), got)
	}

	if got[0].ID != "32" || got[0].Status != "completed" || got[0].HabrID != "1024770" {
		t.Errorf("task[0] = %+v, want id=32 completed habr=1024770", got[0])
	}
	if got[1].ID != "33" || got[1].Status != "pending" || got[1].HabrID != "1030928" {
		t.Errorf("task[1] = %+v, want id=33 pending habr=1030928", got[1])
	}
	if got[2].HabrID != "" {
		t.Errorf("task[2].HabrID = %q, want empty", got[2].HabrID)
	}
	if got[3].HabrID != "999" {
		t.Errorf("task[3].HabrID = %q, want 999 (case-insensitive habr marker)", got[3].HabrID)
	}
}

func TestParseJSON(t *testing.T) {
	t.Run("bare array", func(t *testing.T) {
		const in = `[
			{"id":12,"status":"completed","subject":"habr 555 something"},
			{"task_id":"13","status":"pending","description":"no marker"}
		]`
		got, err := tasklist.ParseJSON(in)
		if err != nil {
			t.Fatalf("ParseJSON: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got[0].ID != "12" || got[0].HabrID != "555" {
			t.Errorf("task[0] = %+v, want id=12 habr=555", got[0])
		}
		if got[1].ID != "13" || got[1].HabrID != "" {
			t.Errorf("task[1] = %+v, want id=13 habr empty", got[1])
		}
	})

	t.Run("wrapped in tasks key", func(t *testing.T) {
		const in = `{"tasks":[{"id":1,"status":"completed","subject":"habr 42 x"}]}`
		got, err := tasklist.ParseJSON(in)
		if err != nil {
			t.Fatalf("ParseJSON: %v", err)
		}
		if len(got) != 1 || got[0].HabrID != "42" {
			t.Fatalf("got %+v, want one task with habr 42", got)
		}
	})

	t.Run("malformed errors", func(t *testing.T) {
		if _, err := tasklist.ParseJSON("not json"); err == nil {
			t.Fatal("expected error for malformed json")
		}
	})
}
