package catalogjson

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// DriftRecord is one link check to be recorded in the catalog.
type DriftRecord struct {
	EntryID   int
	CheckedAt time.Time
	Code      int
}

// ApplyDrift records link checks in the catalog and returns how many entries
// were updated.
//
// Without this the scan is a claim that disappears when the terminal closes:
// the base would keep no memory of what was checked or when, which is how 527
// entries came to have a url nobody had ever asked about.
//
// Shape follows what the catalog already stores: drift_check_date on every
// checked entry, drift_http_code only when the answer was not 200. Either every
// record lands or none does.
func ApplyDrift(path string, records []DriftRecord) (int, error) {
	if len(records) == 0 {
		return 0, nil
	}

	members, entries, err := readEntries(path)
	if err != nil {
		return 0, err
	}

	byID := make(map[int]DriftRecord, len(records))
	for _, r := range records {
		byID[r.EntryID] = r
	}

	updated := 0
	for i, raw := range entries {
		id, err := entryID(raw)
		if err != nil {
			return 0, err
		}
		rec, ok := byID[id]
		if !ok {
			continue
		}
		edited, err := applyDriftToEntry(raw, rec)
		if err != nil {
			return 0, fmt.Errorf("entry %d: %w", id, err)
		}
		entries[i] = edited
		delete(byID, id)
		updated++
	}

	if len(byID) > 0 {
		missing := make([]int, 0, len(byID))
		for id := range byID {
			missing = append(missing, id)
		}
		slices.Sort(missing)
		return 0, fmt.Errorf("no entry with id %v — nothing was written", missing)
	}

	doc, err := assemble(members, entries)
	if err != nil {
		return 0, err
	}
	if err := writeFileAtomic(path, doc); err != nil {
		return 0, err
	}
	return updated, nil
}

func applyDriftToEntry(raw json.RawMessage, rec DriftRecord) (json.RawMessage, error) {
	members, err := readTopLevel(raw)
	if err != nil {
		return nil, err
	}

	date, err := marshalNoEscape(rec.CheckedAt.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	members = setMember(members, "drift_check_date", date)

	// A 200 clears whatever code was stored: an entry that answered 403 in May
	// and answers 200 now must stop asserting a problem this check disproved.
	if rec.Code == 200 {
		return assembleObject(dropMember(members, "drift_http_code"))
	}
	code, err := marshalNoEscape(rec.Code)
	if err != nil {
		return nil, err
	}
	return assembleObject(setMember(members, "drift_http_code", code))
}
