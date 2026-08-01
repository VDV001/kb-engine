package catalogjson

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// WriteupMigration is what MigrateWriteups would do, or did.
type WriteupMigration struct {
	// Created is one entry per write-up file, in the order the files first
	// appear in the catalog.
	Created []WriteupEntry
	// Moved counts the articles that gave up their file for a link.
	Moved int
}

// WriteupEntry names one write-up that became an entry of its own.
type WriteupEntry struct {
	ID    int
	File  string
	Title string
	Count int // how many articles point at it
}

// writeupCategory is where the write-ups land. Their own category rather than
// "creations": a deep-read of someone else's article is not a piece of work the
// owner publishes, and mixing the two would put ninety notes into a portfolio
// of thirteen.
const writeupCategory = "writeups"

// MigrateWriteups splits the two meanings the file member carried at once.
//
// For an own artefact — a standard, an article, a course module — the file IS
// the entry: it has no address on the internet, and the file is what identifies
// it. For someone else's article the very same member meant something else
// entirely: "this is where my notes about it are". One member, two meanings, and
// the dedup in add could not tell them apart — it refused to create an entry for
// a write-up whose path was already sitting on the articles that cite it.
//
// After this migration the file member means exactly one thing. Each write-up
// becomes an entry that owns its file; every article that used to carry that
// path now links to that entry through related_ids and carries no file at all.
//
// An entry is moved only when it has an address of its own (a url) and is not in
// an owner category. Everything else is left untouched, including deep-reads
// already modelled as their own entries — they are where this is heading.
//
// With apply=false nothing is written and the returned plan says what would
// change. Either every entry is rewritten or none is.
func MigrateWriteups(path string, titleOf func(file string) string, now func() time.Time, apply bool) (WriteupMigration, error) {
	members, entries, err := readEntries(path)
	if err != nil {
		return WriteupMigration{}, err
	}

	movers, order, byFile, maxID, err := planWriteups(entries)
	if err != nil {
		return WriteupMigration{}, err
	}
	if len(order) == 0 {
		return WriteupMigration{}, nil
	}

	plan := WriteupMigration{Moved: len(movers)}
	newID := make(map[string]int, len(order))
	nextID := maxID + 1
	for _, file := range order {
		plan.Created = append(plan.Created, WriteupEntry{
			ID: nextID, File: file, Title: titleOf(file), Count: byFile[file],
		})
		newID[file] = nextID
		nextID++
	}
	if !apply {
		return plan, nil
	}

	for i, m := range movers {
		edited, err := detachWriteup(entries[m.index], newID[m.file])
		if err != nil {
			return WriteupMigration{}, fmt.Errorf("entry %d: %w", movers[i].id, err)
		}
		entries[m.index] = edited
	}
	for _, w := range plan.Created {
		raw, err := writeupEntryJSON(w, now())
		if err != nil {
			return WriteupMigration{}, err
		}
		entries = append(entries, raw)
	}

	doc, err := assemble(members, entries)
	if err != nil {
		return WriteupMigration{}, err
	}
	if err := writeFileAtomic(path, doc); err != nil {
		return WriteupMigration{}, err
	}
	return plan, nil
}

// mover is one article whose file is a pointer to notes rather than identity.
type mover struct {
	index int
	id    int
	file  string
}

// planWriteups finds the articles to move, the write-up files in first-seen
// order, how many articles cite each, and the highest id in use.
func planWriteups(entries []json.RawMessage) ([]mover, []string, map[string]int, int, error) {
	var movers []mover
	var order []string
	byFile := map[string]int{}
	maxID := 0

	for i, raw := range entries {
		var e struct {
			ID       int    `json:"id"`
			URL      string `json:"url"`
			Category string `json:"category"`
			File     string `json:"file"`
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, nil, nil, 0, fmt.Errorf("parse entry %d: %w", i, err)
		}
		maxID = max(maxID, e.ID)
		if e.File == "" || e.URL == "" || isOwnerCategory(e.Category) || isRescuedCopy(e.ID, e.File) {
			continue
		}
		movers = append(movers, mover{index: i, id: e.ID, file: e.File})
		if byFile[e.File] == 0 {
			order = append(order, e.File)
		}
		byFile[e.File]++
	}
	return movers, order, byFile, maxID, nil
}

// isRescuedCopy reports the third meaning the file member turned out to carry,
// found on live data rather than in a fixture: a copy of the article's own text,
// pulled out of the web archive after the original was hidden.
//
// It is the entry's body, not a note about it — which is why it stays. The
// evidence is in the name: all forty such files sit under notes/rescued/ and are
// named after the entry that owns them. Turning one into a "write-up" entry
// would duplicate the article's title and claim the owner wrote the text.
func isRescuedCopy(id int, file string) bool {
	return strings.HasPrefix(file, "notes/rescued/") &&
		strings.HasPrefix(path.Base(file), strconv.Itoa(id)+"_")
}

// isOwnerCategory reports the categories whose entries are the owner's own work
// by definition, where the file is identity and must stay.
func isOwnerCategory(category string) bool {
	return category == "creations" || category == "standards" || category == writeupCategory
}

// detachWriteup removes the file member and adds the link to the write-up,
// keeping any links the entry already had.
func detachWriteup(raw json.RawMessage, writeupID int) (json.RawMessage, error) {
	members, err := readTopLevel(raw)
	if err != nil {
		return nil, err
	}
	related := []int{}
	for _, m := range members {
		if m.key == "related_ids" {
			if err := json.Unmarshal(m.val, &related); err != nil {
				return nil, fmt.Errorf("parse related_ids: %w", err)
			}
		}
	}
	if !slices.Contains(related, writeupID) {
		related = append(related, writeupID)
	}
	encoded, err := json.Marshal(related)
	if err != nil {
		return nil, err
	}
	members = setMember(dropMember(members, "file"), "related_ids", encoded)
	return assembleObject(members)
}

// writeupEntryJSON builds the entry that owns the write-up file. It carries no
// url on purpose: the write-up has no address anywhere but this base.
func writeupEntryJSON(w WriteupEntry, now time.Time) (json.RawMessage, error) {
	return marshalNoEscape(map[string]any{
		"id":          w.ID,
		"title":       w.Title,
		"category":    writeupCategory,
		"description": fmt.Sprintf("Разбор, на который ссылаются %d запис(ей) каталога.", w.Count),
		"url":         "",
		"file":        w.File,
		"tags":        []string{"writeup"},
		"status":      "read",
		"lifecycle":   "active",
		"source":      "internal",
		"date_added":  now.UTC().Format("2006-01-02"),
	})
}
