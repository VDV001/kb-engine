package catalogjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/daniil/kb-engine/internal/domain"
)

// Changes describes an edit to existing entries. A zero value asks for nothing
// and is rejected: a command that reports success without touching anything is
// the same silence this engine has been removing everywhere else.
type Changes struct {
	// Lifecycle replaces the entry's lifecycle. Validated against the domain
	// before a single byte is written.
	Lifecycle string
	// AddTags / RemoveTags edit the tag list in place. Adding a tag that is
	// already there is not an error and not a duplicate — the caller is asking
	// for a state, not for an append.
	AddTags    []string
	RemoveTags []string
	// Related replaces related_ids wholesale. Editing one link out of a list is
	// what the whole list is for: it is short, and a partial edit would need a
	// second flag to say "remove" that nobody would remember.
	Related []int
}

func (c Changes) empty() bool {
	return c.Lifecycle == "" && len(c.AddTags) == 0 && len(c.RemoveTags) == 0 && c.Related == nil
}

// SetFields edits the given entries in the catalog file and returns how many
// were changed.
//
// Entries are rewritten member by member, in the order the file already had
// them: everything the domain does not model — and there is plenty, since the
// catalog outlived several tools — is carried through as raw JSON. That is the
// same guarantee AppendEntries gives for existing entries, and it is the reason
// this lives here rather than in a script that reads the file into a map and
// writes it back.
//
// Either every requested id is found and the file is rewritten, or nothing is
// written at all.
func SetFields(path string, ids []int, ch Changes) (int, error) {
	if err := ch.validate(ids); err != nil {
		return 0, err
	}

	members, entries, err := readEntries(path)
	if err != nil {
		return 0, err
	}

	updated, err := editEntries(entries, ids, ch)
	if err != nil {
		return 0, err
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

func (c Changes) validate(ids []int) error {
	if len(ids) == 0 {
		return errors.New("no entry ids given")
	}
	if c.empty() {
		return errors.New("nothing to change: pass at least one of --lifecycle, --add-tag, --remove-tag, --related")
	}
	if c.Lifecycle != "" {
		// The list of valid states belongs to the domain and is asked for here,
		// so a bad value never reaches the file.
		if _, err := domain.NewLifecycle(c.Lifecycle); err != nil {
			return fmt.Errorf("--lifecycle: %w", err)
		}
	}
	return nil
}

func readEntries(path string) ([]member, []json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read catalog: %w", err)
	}
	members, err := readTopLevel(raw)
	if err != nil {
		return nil, nil, err
	}
	var entries []json.RawMessage
	for _, m := range members {
		if m.key == "entries" {
			if err := json.Unmarshal(m.val, &entries); err != nil {
				return nil, nil, fmt.Errorf("parse entries: %w", err)
			}
		}
	}
	return members, entries, nil
}

// editEntries rewrites the matching entries in place and reports how many.
// A missing id is an error raised before anything is written: a typo in one id
// of fifty must not leave the catalog half-edited with no sign of which half.
func editEntries(entries []json.RawMessage, ids []int, ch Changes) (int, error) {
	wanted := make(map[int]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}

	updated := 0
	for i, e := range entries {
		id, err := entryID(e)
		if err != nil {
			return 0, err
		}
		if !wanted[id] {
			continue
		}
		edited, err := applyChanges(e, ch)
		if err != nil {
			return 0, fmt.Errorf("entry %d: %w", id, err)
		}
		entries[i] = edited
		delete(wanted, id)
		updated++
	}

	if len(wanted) > 0 {
		missing := make([]int, 0, len(wanted))
		for id := range wanted {
			missing = append(missing, id)
		}
		slices.Sort(missing)
		return 0, fmt.Errorf("no entry with id %v — nothing was written", missing)
	}
	return updated, nil
}

func entryID(raw json.RawMessage) (int, error) {
	var head struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return 0, fmt.Errorf("parse entry: %w", err)
	}
	return head.ID, nil
}

// applyChanges rewrites one entry, keeping its key order. A key that already
// exists is replaced in place; a new one is appended, which is where a reader
// looks for it anyway.
func applyChanges(raw json.RawMessage, ch Changes) (json.RawMessage, error) {
	members, err := readTopLevel(raw)
	if err != nil {
		return nil, err
	}

	if ch.Lifecycle != "" {
		encoded, err := marshalNoEscape(ch.Lifecycle)
		if err != nil {
			return nil, err
		}
		members = setMember(members, "lifecycle", encoded)
	}

	if len(ch.AddTags) > 0 || len(ch.RemoveTags) > 0 {
		tags, err := editTags(members, ch)
		if err != nil {
			return nil, err
		}
		encoded, err := marshalNoEscape(tags)
		if err != nil {
			return nil, err
		}
		members = setMember(members, "tags", encoded)
	}

	if ch.Related != nil {
		encoded, err := marshalNoEscape(ch.Related)
		if err != nil {
			return nil, err
		}
		members = setMember(members, "related_ids", encoded)
	}

	return assembleObject(members)
}

func editTags(members []member, ch Changes) ([]string, error) {
	var tags []string
	for _, m := range members {
		if m.key != "tags" {
			continue
		}
		if err := json.Unmarshal(m.val, &tags); err != nil {
			return nil, fmt.Errorf("parse tags: %w", err)
		}
	}
	for _, t := range ch.AddTags {
		if !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	return slices.DeleteFunc(tags, func(t string) bool {
		return slices.Contains(ch.RemoveTags, t)
	}), nil
}

func setMember(members []member, key string, val json.RawMessage) []member {
	for i := range members {
		if members[i].key == key {
			members[i].val = val
			return members
		}
	}
	return append(members, member{key: key, val: val})
}

func assembleObject(members []member) (json.RawMessage, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeKey(&buf, m.key); err != nil {
			return nil, err
		}
		buf.Write(m.val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
