package catalogjson

import (
	"encoding/json"
	"fmt"

	"github.com/daniil/kb-engine/internal/domain"
)

// URLChange is one address the migration would rewrite.
type URLChange struct {
	EntryID int
	Title   string
	From    string
	To      string
}

// MigrateURLs strips campaign tracking from every address in the catalog.
//
// With apply=false nothing is written and the returned list is the plan. The
// address is what an entry is, so the caller shows the result before it runs.
func MigrateURLs(path string, apply bool) ([]URLChange, error) {
	members, entries, err := readEntries(path)
	if err != nil {
		return nil, err
	}

	var changes []URLChange
	for i, raw := range entries {
		var head struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
			URL   string `json:"url"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return nil, fmt.Errorf("parse entry: %w", err)
		}
		cleaned := domain.StripTrackingParams(head.URL)
		if cleaned == head.URL {
			continue
		}
		changes = append(changes, URLChange{EntryID: head.ID, Title: head.Title, From: head.URL, To: cleaned})

		encoded, err := marshalNoEscape(cleaned)
		if err != nil {
			return nil, err
		}
		edited, err := readTopLevel(raw)
		if err != nil {
			return nil, err
		}
		obj, err := assembleObject(setMember(edited, "url", encoded))
		if err != nil {
			return nil, err
		}
		entries[i] = obj
	}

	if !apply || len(changes) == 0 {
		return changes, nil
	}
	doc, err := assemble(members, entries)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(path, doc); err != nil {
		return nil, err
	}
	return changes, nil
}
