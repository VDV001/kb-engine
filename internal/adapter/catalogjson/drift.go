package catalogjson

import (
	"bytes"
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
	// NewURL is where a redirect points. Written only by ApplyDriftWithURLs.
	NewURL string
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
	return applyDrift(path, records, false)
}

// ApplyDriftWithURLs does the same and additionally replaces the entry's url
// with the redirect target. Separated from ApplyDrift on purpose: the address
// is what the entry is, and rewriting it must be something the owner asked for.
func ApplyDriftWithURLs(path string, records []DriftRecord) (int, error) {
	return applyDrift(path, records, true)
}

func applyDrift(path string, records []DriftRecord, withURLs bool) (int, error) {
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
		edited, changed, err := applyDriftToEntry(raw, rec, withURLs)
		if err != nil {
			return 0, fmt.Errorf("entry %d: %w", id, err)
		}
		entries[i] = edited
		// Счёт идёт по ИЗМЕНИВШИМСЯ записям, а не по совпавшим с записями
		// прогона. Повторный прогон в тот же день кладёт те же значения:
		// файл не меняется ни на байт, и «записано N» тогда означало бы
		// успех без содержания — ровно то, что запрещает гейт против
		// ложного успеха.
		if changed {
			updated++
		}
		delete(byID, id)
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

// applyDriftToEntry returns the edited entry and whether it differs from the
// original. Сравнение идёт КАНОНА С КАНОНОМ: сырой элемент несёт отступы из
// файла, а сборка выдаёт единообразный вид, поэтому побайтовое сравнение
// сырого с собранным объявляло бы изменившимся всё подряд.
func applyDriftToEntry(raw json.RawMessage, rec DriftRecord, withURLs bool) (json.RawMessage, bool, error) {
	members, err := readTopLevel(raw)
	if err != nil {
		return nil, false, err
	}
	before, err := assembleObject(members)
	if err != nil {
		return nil, false, err
	}

	if withURLs && rec.NewURL != "" {
		url, err := marshalNoEscape(rec.NewURL)
		if err != nil {
			return nil, false, err
		}
		members = setMember(members, "url", url)
	}

	date, err := marshalNoEscape(rec.CheckedAt.Format("2006-01-02"))
	if err != nil {
		return nil, false, err
	}
	members = setMember(members, "drift_check_date", date)

	// A 200 clears whatever code was stored: an entry that answered 403 in May
	// and answers 200 now must stop asserting a problem this check disproved.
	if rec.Code == 200 {
		members = dropMember(members, "drift_http_code")
	} else {
		code, err := marshalNoEscape(rec.Code)
		if err != nil {
			return nil, false, err
		}
		members = setMember(members, "drift_http_code", code)
	}
	edited, err := assembleObject(members)
	if err != nil {
		return nil, false, err
	}
	return edited, !bytes.Equal(before, edited), nil
}
