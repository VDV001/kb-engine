package changelog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/changelog"
)

func TestDocumentMarshalJSON(t *testing.T) {
	doc := changelog.Parse(sample)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(raw)

	// Sections must serialize as an ordered object (parity with the Python tool),
	// not an array.
	if !strings.Contains(text, `"sections":{"Added":["feature A","feature B"],"Changed":`) {
		t.Errorf("sections not an ordered object:\n%s", text)
	}
	// Unreleased has a null date.
	if !strings.Contains(text, `"version":"Unreleased","date":null`) {
		t.Errorf("unreleased date should be null:\n%s", text)
	}
	// Convenience fields present.
	if !strings.Contains(text, `"current_version":"1.5.0"`) {
		t.Errorf("missing current_version:\n%s", text)
	}
	if !strings.Contains(text, `"_comment":`) {
		t.Errorf("missing _comment:\n%s", text)
	}

	// And it must still be valid JSON that round-trips structurally.
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if back["current_date"] != "2026-05-02" {
		t.Errorf("current_date = %v", back["current_date"])
	}
}

func TestDocumentMarshalJSON_NoReleases(t *testing.T) {
	doc := changelog.Parse("# nothing\n")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"current_version":"0.0.0"`) {
		t.Errorf("want 0.0.0 fallback:\n%s", text)
	}
	if !strings.Contains(text, `"current_date":null`) {
		t.Errorf("want null current_date:\n%s", text)
	}
	if !strings.Contains(text, `"releases":[]`) {
		t.Errorf("want empty releases array:\n%s", text)
	}
}
