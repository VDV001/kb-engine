package catalogjson

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// VersionMigration is what MigrateVersions would do, or did. Moved lists the
// entries whose legacy version became a revision; Undecidable lists own
// artefacts whose version is a bare number that cannot be widened to a semver
// without guessing.
type VersionMigration struct {
	Moved       []int
	Undecidable []VersionConflict
}

// VersionConflict names one entry the migration refuses to decide for, and what
// it found there.
type VersionConflict struct {
	ID      int
	Stored  string
	Title   string
	Because string
}

// MigrateVersions moves the legacy version of someone else's material into the
// revision field. Own artefacts are left alone: their version is a semver that
// also lives in the artefact file, and the audit compares the two.
//
// With apply=false nothing is written and the returned plan says what would
// change. Either every entry is rewritten or none is — the same all-or-nothing
// guarantee SetFields gives.
func MigrateVersions(path string, apply bool) (VersionMigration, error) {
	members, entries, err := readEntries(path)
	if err != nil {
		return VersionMigration{}, err
	}

	var plan VersionMigration
	rewritten := make(map[int]json.RawMessage, len(entries))

	for i, raw := range entries {
		id, err := entryID(raw)
		if err != nil {
			return VersionMigration{}, err
		}
		decision, err := decideVersion(raw, id)
		if err != nil {
			return VersionMigration{}, fmt.Errorf("entry %d: %w", id, err)
		}
		switch {
		case decision.conflict != nil:
			plan.Undecidable = append(plan.Undecidable, *decision.conflict)
		case decision.revision > 0:
			plan.Moved = append(plan.Moved, id)
			edited, err := moveVersionToRevision(raw, decision.revision)
			if err != nil {
				return VersionMigration{}, fmt.Errorf("entry %d: %w", id, err)
			}
			rewritten[i] = edited
		}
	}

	if len(plan.Undecidable) > 0 {
		return plan, nil // the caller reports and stops; nothing was written
	}
	if !apply || len(plan.Moved) == 0 {
		return plan, nil
	}

	for i, edited := range rewritten {
		entries[i] = edited
	}
	doc, err := assemble(members, entries)
	if err != nil {
		return VersionMigration{}, err
	}
	if err := writeFileAtomic(path, doc); err != nil {
		return VersionMigration{}, err
	}
	return plan, nil
}

// versionDecision is what one entry needs: a revision to move to, a conflict
// only the owner can settle, or neither.
type versionDecision struct {
	revision int
	conflict *VersionConflict
}

// decideVersion inspects an entry's stored members. It works on the raw JSON
// rather than a loaded domain entry because the migration must rewrite the file
// member by member, preserving everything the domain does not model.
func decideVersion(raw json.RawMessage, id int) (versionDecision, error) {
	var head struct {
		Version  json.RawMessage `json:"version"`
		Title    string          `json:"title"`
		Category string          `json:"category"`
		File     string          `json:"file"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return versionDecision{}, fmt.Errorf("parse entry: %w", err)
	}

	stored := strings.TrimSpace(string(head.Version))
	if stored == "" || stored == "null" {
		return versionDecision{}, nil
	}
	var s string
	if err := json.Unmarshal(head.Version, &s); err != nil {
		s = stored // a bare JSON number arrives unquoted
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return versionDecision{}, nil
	}

	own := domain.IsOwnArtefact(head.Category, head.File)
	if _, err := domain.NewVersion(s); err == nil {
		if own {
			return versionDecision{}, nil // already correct
		}
		// Someone else's material carrying a full semver: not seen in the live
		// catalog, and not something to decide silently either way.
		return versionDecision{conflict: &VersionConflict{
			ID: id, Stored: s, Title: head.Title,
			Because: "чужой материал с полным семвером — версия чужого артефакта, а не редакция карточки",
		}}, nil
	}

	head2, _, _ := strings.Cut(s, ".")
	n, err := strconv.Atoi(head2)
	if err != nil || n < 1 {
		return versionDecision{}, fmt.Errorf("version %q is neither a semver nor a revision number", s)
	}
	if own {
		return versionDecision{conflict: &VersionConflict{
			ID: id, Stored: s, Title: head.Title,
			Because: "свой артефакт с голым числом — семвер нельзя достроить догадкой",
		}}, nil
	}
	return versionDecision{revision: n}, nil
}

// moveVersionToRevision drops the version member and writes revision in its
// place, keeping every other member where it was.
func moveVersionToRevision(raw json.RawMessage, revision int) (json.RawMessage, error) {
	members, err := readTopLevel(raw)
	if err != nil {
		return nil, err
	}
	encoded, err := marshalNoEscape(revision)
	if err != nil {
		return nil, err
	}
	out := make([]member, 0, len(members))
	replaced := false
	for _, m := range members {
		if m.key == "version" {
			out = append(out, member{key: "revision", val: encoded})
			replaced = true
			continue
		}
		if m.key == "revision" {
			continue // the version member carries the value now
		}
		out = append(out, m)
	}
	if !replaced {
		out = append(out, member{key: "revision", val: encoded})
	}
	return assembleObject(out)
}
