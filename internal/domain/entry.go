package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidEntry is returned when an entry violates an invariant.
var ErrInvalidEntry = errors.New("invalid entry")

// ErrUnknownKind is returned when an entry's kind has no registered spec.
var ErrUnknownKind = errors.New("unknown entry kind")

// Entry kinds.
const (
	KindArticle  = "article"
	KindCreation = "creation"
)

// kindSpec declares which optional aspects a kind requires. To add a new entry
// kind, add one row to kindSpecs below — NewEntry needs no other change.
type kindSpec struct {
	requireReadState    bool
	requirePublishStage bool
}

var kindSpecs = map[string]kindSpec{
	// An article always carries a read/unread triage state; a verdict appears
	// only once read and decided. habr_id and url are optional — many real
	// entries (bot-inbox links) have neither.
	KindArticle:  {requireReadState: true},
	KindCreation: {requirePublishStage: true},
}

// EntryParams is the input to NewEntry. Value-object fields must already be
// constructed (hence valid); optional aspects are nil when absent.
type EntryParams struct {
	ID            int
	Kind          string
	Title         string
	Category      Category
	Lifecycle     Lifecycle
	HabrID        *int
	URL           string
	Verdict       *Verdict
	ReadState     *ReadState
	PublishStage  *PublishStage
	Tags          []string
	Description   string
	Source        string
	Author        string
	Notes         string
	NotesFile     string
	IsTranslation bool
	SupersedesID  *int
	RelatedIDs    []int
	DateAdded     *time.Time
	DateCreated   *time.Time
	// Version is the semver of an artefact the owner versions himself; Revision
	// counts editions of the card for someone else's material. At most one may
	// be set — see checkVersioning.
	Version  *Version
	Revision *int
	// SourceBatch is the import batch the entry arrived in, and SourceDate when
	// that material was published. Both are determined by the batch and stored
	// on every entry — a deliberate denormalization (ADR-0002) guarded by the
	// batch-consistency audit.
	SourceBatch *int
	SourceDate  *time.Time
	// DriftCheckDate is when the entry's url was last asked for its status.
	// Absent means never — which is a fact the base has to be able to state.
	DriftCheckDate *time.Time
}

// Entry is the central KB entity. Construct it via NewEntry, which enforces the
// common invariants and the kind-specific requirements.
type Entry struct {
	id             int
	kind           string
	title          string
	category       Category
	lifecycle      Lifecycle
	habrID         *int
	url            string
	verdict        *Verdict
	readState      *ReadState
	publishStage   *PublishStage
	tags           []string
	description    string
	source         string
	author         string
	notes          string
	notesFile      string
	isTranslation  bool
	supersedesID   *int
	relatedIDs     []int
	dateAdded      *time.Time
	dateCreated    *time.Time
	version        *Version
	revision       *int
	sourceBatch    *int
	sourceDate     *time.Time
	driftCheckDate *time.Time
}

// NewEntry validates p and returns an Entry. Common invariants are checked
// first, then the requirements declared by the kind's spec.
func NewEntry(p EntryParams) (Entry, error) {
	spec, ok := kindSpecs[p.Kind]
	if !ok {
		return Entry{}, fmt.Errorf("%w: %q", ErrUnknownKind, p.Kind)
	}
	if p.ID <= 0 {
		return Entry{}, fmt.Errorf("%w: id must be positive, got %d", ErrInvalidEntry, p.ID)
	}
	if strings.TrimSpace(p.Title) == "" {
		return Entry{}, fmt.Errorf("%w: title must not be empty", ErrInvalidEntry)
	}
	if err := checkRequired(p, spec); err != nil {
		return Entry{}, err
	}
	if err := checkVersioning(p); err != nil {
		return Entry{}, err
	}
	return Entry{
		id:             p.ID,
		kind:           p.Kind,
		title:          p.Title,
		category:       p.Category,
		lifecycle:      p.Lifecycle,
		habrID:         p.HabrID,
		url:            p.URL,
		verdict:        p.Verdict,
		readState:      p.ReadState,
		publishStage:   p.PublishStage,
		tags:           cloneTags(p.Tags),
		description:    p.Description,
		source:         p.Source,
		author:         p.Author,
		notes:          p.Notes,
		notesFile:      p.NotesFile,
		isTranslation:  p.IsTranslation,
		supersedesID:   clonePtrInt(p.SupersedesID),
		relatedIDs:     cloneInts(p.RelatedIDs),
		dateAdded:      clonePtrTime(p.DateAdded),
		dateCreated:    clonePtrTime(p.DateCreated),
		version:        clonePtrVersion(p.Version),
		revision:       clonePtrInt(p.Revision),
		sourceBatch:    clonePtrInt(p.SourceBatch),
		sourceDate:     clonePtrTime(p.SourceDate),
		driftCheckDate: clonePtrTime(p.DriftCheckDate),
	}, nil
}

// checkVersioning enforces that an entry carries at most one notion of version.
// Both at once is what the single legacy field allowed, and the live catalog
// shows where that leads: one artefact versioned as "2.0.0" in one entry and as
// 5 in another.
func checkVersioning(p EntryParams) error {
	if p.Version != nil && p.Revision != nil {
		return fmt.Errorf("%w: version %s and revision %d are both set; an entry carries one or the other",
			ErrInvalidEntry, p.Version, *p.Revision)
	}
	if p.Revision != nil && *p.Revision < 1 {
		return fmt.Errorf("%w: revision must be positive, got %d", ErrInvalidEntry, *p.Revision)
	}
	return nil
}

// cloneTags returns an independent copy so the entity does not alias the
// caller's slice. nil in stays nil out.
func cloneTags(tags []string) []string {
	if tags == nil {
		return nil
	}
	return append([]string(nil), tags...)
}

func cloneInts(s []int) []int {
	if s == nil {
		return nil
	}
	return append([]int(nil), s...)
}

func clonePtrInt(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func clonePtrVersion(p *Version) *Version {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func clonePtrTime(p *time.Time) *time.Time {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func checkRequired(p EntryParams, spec kindSpec) error {
	if spec.requireReadState && p.ReadState == nil {
		return fmt.Errorf("%w: kind %q requires read state", ErrInvalidEntry, p.Kind)
	}
	if spec.requirePublishStage && p.PublishStage == nil {
		return fmt.Errorf("%w: kind %q requires publish stage", ErrInvalidEntry, p.Kind)
	}
	return nil
}

// ID returns the entry's numeric identifier.
func (e Entry) ID() int { return e.id }

// Kind returns the entry's kind.
func (e Entry) Kind() string { return e.kind }

// Title returns the entry's title.
func (e Entry) Title() string { return e.title }

// Category returns the entry's category.
func (e Entry) Category() Category { return e.category }

// Lifecycle returns the entry's lifecycle state.
func (e Entry) Lifecycle() Lifecycle { return e.lifecycle }

// HabrID returns the Habr article id, or nil if absent.
func (e Entry) HabrID() *int { return e.habrID }

// URL returns the entry's URL (may be empty for kinds that don't require it).
func (e Entry) URL() string { return e.url }

// Verdict returns the review verdict, or nil if absent.
func (e Entry) Verdict() *Verdict { return e.verdict }

// ReadState returns the read state, or nil if absent.
func (e Entry) ReadState() *ReadState { return e.readState }

// PublishStage returns the publish stage, or nil if absent.
func (e Entry) PublishStage() *PublishStage { return e.publishStage }

// Tags returns a copy of the entry's tags, so callers cannot mutate the
// entity's internal state.
func (e Entry) Tags() []string { return cloneTags(e.tags) }

// Description returns the entry's description.
func (e Entry) Description() string { return e.description }

// Source returns the entry's source (e.g. "bot-inbox", "manual").
func (e Entry) Source() string { return e.source }

// Author returns the entry's author.
func (e Entry) Author() string { return e.author }

// Notes returns the entry's free-form notes.
func (e Entry) Notes() string { return e.notes }

// NotesFile returns the path to the entry's write-up, or "" when none was
// written. It is the one signal for «этот материал разобран, а не только
// прочитан» that the catalog carries.
func (e Entry) NotesFile() string { return e.notesFile }

// IsTranslation reports whether the entry is a translation of a foreign
// original. Absent the field the answer is «no», which is what 1280 of the
// 1340 catalog entries mean by staying silent about it.
func (e Entry) IsTranslation() bool { return e.isTranslation }

// SupersedesID returns the id this entry supersedes, or nil.
func (e Entry) SupersedesID() *int { return clonePtrInt(e.supersedesID) }

// RelatedIDs returns a copy of the related entry ids.
func (e Entry) RelatedIDs() []int { return cloneInts(e.relatedIDs) }

// DateAdded returns when the entry was added to the catalog, or nil.
func (e Entry) DateAdded() *time.Time { return clonePtrTime(e.dateAdded) }

// DateCreated returns when the source content was created, or nil.
func (e Entry) DateCreated() *time.Time { return clonePtrTime(e.dateCreated) }

// ownArtefactTrees are the paths under which the owner's own writing lives.
var ownArtefactTrees = []string{"standards/", "creations/", "docs/"}

// ownArtefactCategories are the categories that are owner output by definition.
//
// "writeups" holds the owner's notes on someone else's material: he wrote the
// note and versions it, so its file is its identity — which is exactly what
// separates the note from the article it is about. Before the write-ups became
// entries of their own, both meanings sat on the article's card at once.
var ownArtefactCategories = map[string]struct{}{"creations": {}, "standards": {}, "writeups": {}}

// IsOwnArtefact reports whether the entry is something the owner wrote and
// versions himself — a standard, an article draft, a course module, a deep-read
// write-up — as opposed to material collected from elsewhere.
//
// It decides which notion of version the entry may carry: an own artefact has a
// semver that also lives in the file itself; someone else's material has at
// most a revision counter for the card.
func (e Entry) IsOwnArtefact() bool {
	return IsOwnArtefact(e.category.String(), e.notesFile)
}

// IsOwnArtefact answers the same question for a category and file path that have
// not been built into an Entry yet — the migration works on raw JSON members and
// needs the rule before the loader runs. One implementation, so the two callers
// cannot drift apart.
func IsOwnArtefact(category, file string) bool {
	if _, ok := ownArtefactCategories[category]; ok {
		return true
	}
	for _, tree := range ownArtefactTrees {
		if strings.HasPrefix(file, tree) {
			return true
		}
	}
	return false
}

// Version returns the semver of an owner artefact, or nil when the entry is not
// one. The catalog's copy can fall behind the artefact file, which is what the
// version audit compares.
func (e Entry) Version() *Version { return clonePtrVersion(e.version) }

// Revision returns how many times the card for someone else's material was
// rewritten, or nil when it was never rewritten.
func (e Entry) Revision() *int { return clonePtrInt(e.revision) }

// SourceBatch returns the import batch the entry arrived in, or nil for entries
// added outside a batch.
func (e Entry) SourceBatch() *int { return clonePtrInt(e.sourceBatch) }

// SourceDate returns when the source material was published, or nil.
func (e Entry) SourceDate() *time.Time { return clonePtrTime(e.sourceDate) }

// DriftCheckDate returns when the entry's url was last checked, or nil when it
// never was.
func (e Entry) DriftCheckDate() *time.Time { return clonePtrTime(e.driftCheckDate) }
