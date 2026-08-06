package catalogjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
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
	// Version sets the semver of an own artefact; Revision sets the edition
	// counter of someone else's card. Writing either clears the other, because
	// an entry holding both is one the domain refuses to load.
	Version  string
	Revision int
	// Verdict replaces the entry's triage verdict. It writes the legacy status
	// field, but only with a value that IS a verdict — not with a read state or
	// a publish stage, which the same field also carries for other kinds.
	Verdict string
	// NotesFile points at the write-up inside the knowledge base; URL points at
	// the original material outside it. They are separate flags because the
	// catalog already proved they get confused: several entries carry a file
	// path in url and nothing in file.
	NotesFile string
	// Category moves an entry between categories. Validated by the domain like
	// every other value: a category the loader would reject must not reach the
	// file.
	Category string
	URL      string
	// ClearURL removes the url instead of replacing it. Kept apart from URL
	// because an empty string means "the flag was not passed", and a forgotten
	// flag must never wipe a field.
	ClearURL bool
	// Notes and Author describe the entry and may be set on a group: a series by
	// one author, one note across a batch. Title, Description and SupersedesID
	// belong to a single entry, and writing them to several at once is a mistake
	// in the command line rather than an intention.
	Notes  string
	Author string
	// Source — откуда запись пришла: digest, bot-inbox, batch, ad-hoc. Групповое
	// поле, как Notes и Author: одна партия имеет один источник. Правится, потому
	// что раньше проставлялось руками и расходилось молча — семь записей от
	// 04.08.2026 пришли из дайджеста и несли "bot-inbox", из-за чего счёт
	// «сколько дал дайджест» занижался и не имел способа быть исправленным
	// иначе, чем правкой JSON мимо движка.
	Source       string
	Title        string
	Description  string
	SupersedesID *int
}

func (c Changes) empty() bool {
	// Listed rather than compared against a zero value: Changes holds slices,
	// and a struct with slices is not comparable.
	for _, text := range []string{c.Lifecycle, c.Version, c.Verdict, c.NotesFile, c.URL, c.Category,
		c.Notes, c.Author, c.Title, c.Description, c.Source} {
		if text != "" {
			return false
		}
	}
	return len(c.AddTags) == 0 && len(c.RemoveTags) == 0 && c.Related == nil &&
		c.Revision == 0 && !c.ClearURL && c.SupersedesID == nil
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

	if err := checkSupersedes(entries, ids, ch); err != nil {
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
		return errors.New("nothing to change: pass at least one of --lifecycle, --add-tag, --remove-tag, --related, --version, --revision, --verdict, --file, --url")
	}
	if err := c.validateVersioning(); err != nil {
		return err
	}
	if err := c.validateLocations(); err != nil {
		return err
	}
	if err := c.validateSingleEntryFields(ids); err != nil {
		return err
	}
	// The lists of valid values belong to the domain and are asked for here, so
	// a bad one never reaches the file.
	if c.Verdict != "" {
		if _, err := domain.NewVerdict(c.Verdict); err != nil {
			return fmt.Errorf("--verdict: %w", err)
		}
	}
	if c.Lifecycle != "" {
		if _, err := domain.NewLifecycle(c.Lifecycle); err != nil {
			return fmt.Errorf("--lifecycle: %w", err)
		}
	}
	return nil
}

func (c Changes) validateVersioning() error {
	if c.Version != "" && c.Revision != 0 {
		return errors.New("--version and --revision are mutually exclusive: an entry carries one or the other")
	}
	if c.Version != "" {
		if _, err := domain.NewVersion(c.Version); err != nil {
			return fmt.Errorf("--version: %w", err)
		}
	}
	if c.Revision < 0 {
		return fmt.Errorf("--revision: must be positive, got %d", c.Revision)
	}
	return nil
}

// validateSingleEntryFields guards the fields that describe one entry and not a
// group. A title written to fifty entries at once is not an edit anyone meant.
func (c Changes) validateSingleEntryFields(ids []int) error {
	if len(ids) == 1 {
		return nil
	}
	for name, set := range map[string]bool{
		"--title": c.Title != "", "--description": c.Description != "", "--supersedes": c.SupersedesID != nil,
	} {
		if set {
			return fmt.Errorf("%s describes one entry: pass a single id, got %d", name, len(ids))
		}
	}
	return nil
}

func (c Changes) validateLocations() error {
	if c.ClearURL && c.URL != "" {
		return errors.New("--url and clearing the url are opposite instructions: pass one")
	}
	if c.URL != "" {
		if _, err := domain.NewExternalURL(c.URL); err != nil {
			return fmt.Errorf("--url: %w", err)
		}
	}
	if c.NotesFile != "" {
		if _, err := domain.NewNotesPath(c.NotesFile); err != nil {
			return fmt.Errorf("--file: %w", err)
		}
	}
	if c.Category != "" {
		if _, err := domain.NewCategory(c.Category); err != nil {
			return fmt.Errorf("--category: %w", err)
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
// checkSupersedes verifies the link points at an entry that exists and is not
// the entry itself. Both are exactly what --check integrity reports later; the
// cheaper moment to notice is before the write.
func checkSupersedes(entries []json.RawMessage, ids []int, ch Changes) error {
	if ch.SupersedesID == nil {
		return nil
	}
	target := *ch.SupersedesID
	if len(ids) == 1 && ids[0] == target {
		return fmt.Errorf("--supersedes: entry %d cannot supersede itself", target)
	}
	for _, raw := range entries {
		id, err := entryID(raw)
		if err != nil {
			return err
		}
		if id == target {
			return nil
		}
	}
	return fmt.Errorf("--supersedes: no entry with id %d — the link would point nowhere", target)
}

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
		// Считается изменение, а не попытка. Записи, которой присвоили то, что у
		// неё уже стояло, правка ничего не сделала — и «1 обновлена» на ней было
		// бы «выполнено» без содержания: после такого ответа никто не приходит
		// проверять.
		//
		// Сравнение по содержанию, а не по байтам: пересборка JSON меняет их и
		// там, где значения остались прежними (это видно на добавлении тега,
		// который уже стоит).
		if changedInSubstance(e, edited) {
			updated++
		}
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

	members, err = applyTags(members, ch)
	if err != nil {
		return nil, err
	}

	if ch.Related != nil {
		encoded, err := marshalNoEscape(ch.Related)
		if err != nil {
			return nil, err
		}
		members = setMember(members, "related_ids", encoded)
	}

	members, err = applyVersioning(members, ch)
	if err != nil {
		return nil, err
	}

	members, err = applyVerdict(members, ch.Verdict)
	if err != nil {
		return nil, err
	}

	members, err = applyLocations(members, ch)
	if err != nil {
		return nil, err
	}

	members, err = applyText(members, ch)
	if err != nil {
		return nil, err
	}

	return assembleObject(members)
}

// applyText writes the free-text fields and the supersedes link. Each is skipped
// when not asked for: an empty string means the flag was absent, never "erase".
func applyText(members []member, ch Changes) ([]member, error) {
	for _, f := range []struct{ key, raw string }{
		{"notes", ch.Notes}, {"author", ch.Author}, {"source", ch.Source},
		{"title", ch.Title}, {"description", ch.Description},
		{"category", ch.Category},
	} {
		if f.raw == "" {
			continue
		}
		encoded, err := marshalNoEscape(f.raw)
		if err != nil {
			return nil, err
		}
		members = setMember(members, f.key, encoded)
	}
	if ch.SupersedesID != nil {
		encoded, err := marshalNoEscape(*ch.SupersedesID)
		if err != nil {
			return nil, err
		}
		members = setMember(members, "supersedes_id", encoded)
	}
	return members, nil
}

// applyTags rewrites the tag list when either tag flag was passed.
func applyTags(members []member, ch Changes) ([]member, error) {
	if len(ch.AddTags) == 0 && len(ch.RemoveTags) == 0 {
		return members, nil
	}
	tags, err := editTags(members, ch)
	if err != nil {
		return nil, err
	}
	encoded, err := marshalNoEscape(tags)
	if err != nil {
		return nil, err
	}
	return setMember(members, "tags", encoded), nil
}

// applyLocations writes the two reference fields. Validation already ruled out
// a path in url and an escaping file, so the values are normalised copies of
// what the domain accepted.
func applyLocations(members []member, ch Changes) ([]member, error) {
	if ch.ClearURL {
		members = dropMember(members, "url")
	}
	for _, f := range []struct {
		key, raw string
	}{{"file", ch.NotesFile}, {"url", ch.URL}} {
		if f.raw == "" {
			continue
		}
		normalised := f.raw
		if f.key == "file" {
			p, err := domain.NewNotesPath(f.raw)
			if err != nil {
				return nil, err
			}
			normalised = p.String()
		}
		encoded, err := marshalNoEscape(normalised)
		if err != nil {
			return nil, err
		}
		members = setMember(members, f.key, encoded)
	}
	return members, nil
}

// applyVerdict replaces the entry's verdict, refusing to overwrite a publish
// stage. The legacy status field holds three different axes depending on the
// entry's kind, and writing a verdict over a stage would silently change what
// kind of thing the entry is.
func applyVerdict(members []member, verdict string) ([]member, error) {
	if verdict == "" {
		return members, nil
	}
	for _, m := range members {
		if m.key != "status" {
			continue
		}
		var current string
		if err := json.Unmarshal(m.val, &current); err != nil {
			return nil, fmt.Errorf("parse status: %w", err)
		}
		if _, err := domain.NewPublishStage(current); err == nil {
			return nil, fmt.Errorf("status %q is a publish stage, not a verdict — refusing to change what kind of entry this is", current)
		}
	}
	encoded, err := marshalNoEscape(verdict)
	if err != nil {
		return nil, err
	}
	return setMember(members, "status", encoded), nil
}

// applyVersioning writes whichever of the two version fields was asked for and
// removes the other. Validation already ruled out both at once.
func applyVersioning(members []member, ch Changes) ([]member, error) {
	switch {
	case ch.Version != "":
		encoded, err := marshalNoEscape(ch.Version)
		if err != nil {
			return nil, err
		}
		return setMember(dropMember(members, "revision"), "version", encoded), nil
	case ch.Revision != 0:
		encoded, err := marshalNoEscape(ch.Revision)
		if err != nil {
			return nil, err
		}
		return setMember(dropMember(members, "version"), "revision", encoded), nil
	default:
		return members, nil
	}
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

// dropMember removes a key entirely. Setting it to null would leave the reader
// with a member that has to be checked for emptiness everywhere it is read.
func dropMember(members []member, key string) []member {
	return slices.DeleteFunc(members, func(m member) bool { return m.key == key })
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

// changedInSubstance отвечает, различаются ли две записи по содержанию.
//
// Байты не годятся: пересборка JSON переставляет ключи и меняет отступы даже
// там, где значения те же. Разобранное представление сравнивается целиком, со
// всеми полями, включая те, о которых домен не знает, — иначе правка чужого
// поля прошла бы как «ничего не изменилось».
//
// Не разобравшуюся запись считаем изменённой: соврать «изменений нет» здесь
// хуже, чем лишний раз сказать «изменено».
func changedInSubstance(before, after json.RawMessage) bool {
	var a, b any
	if err := json.Unmarshal(before, &a); err != nil {
		return true
	}
	if err := json.Unmarshal(after, &b); err != nil {
		return true
	}
	return !reflect.DeepEqual(a, b)
}
