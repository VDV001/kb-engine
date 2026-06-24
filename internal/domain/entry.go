package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidEntry is returned when an entry violates an invariant.
var ErrInvalidEntry = errors.New("invalid entry")

// ErrUnknownKind is returned when an entry's kind has no registered spec.
var ErrUnknownKind = errors.New("unknown entry kind")

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
	"article":  {requireReadState: true},
	"creation": {requirePublishStage: true},
}

// EntryParams is the input to NewEntry. Value-object fields must already be
// constructed (hence valid); optional aspects are nil when absent.
type EntryParams struct {
	ID           int
	Kind         string
	Title        string
	Category     Category
	Lifecycle    Lifecycle
	HabrID       *int
	URL          string
	Verdict      *Verdict
	ReadState    *ReadState
	PublishStage *PublishStage
	Tags         []string
	Description  string
}

// Entry is the central KB entity. Construct it via NewEntry, which enforces the
// common invariants and the kind-specific requirements.
type Entry struct {
	id           int
	kind         string
	title        string
	category     Category
	lifecycle    Lifecycle
	habrID       *int
	url          string
	verdict      *Verdict
	readState    *ReadState
	publishStage *PublishStage
	tags         []string
	description  string
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
	return Entry{
		id:           p.ID,
		kind:         p.Kind,
		title:        p.Title,
		category:     p.Category,
		lifecycle:    p.Lifecycle,
		habrID:       p.HabrID,
		url:          p.URL,
		verdict:      p.Verdict,
		readState:    p.ReadState,
		publishStage: p.PublishStage,
		tags:         cloneTags(p.Tags),
		description:  p.Description,
	}, nil
}

// cloneTags returns an independent copy so the entity does not alias the
// caller's slice. nil in stays nil out.
func cloneTags(tags []string) []string {
	if tags == nil {
		return nil
	}
	return append([]string(nil), tags...)
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
