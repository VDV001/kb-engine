package httpapi

import "github.com/daniil/kb-engine/internal/domain"

// entryDTO is the JSON shape of an entry exposed by the API. Optional aspects
// are omitted when absent.
type entryDTO struct {
	ID           int      `json:"id"`
	HabrID       *int     `json:"habr_id,omitempty"`
	Title        string   `json:"title"`
	URL          string   `json:"url,omitempty"`
	Category     string   `json:"category"`
	Kind         string   `json:"kind"`
	Lifecycle    string   `json:"lifecycle"`
	Verdict      *string  `json:"verdict,omitempty"`
	ReadState    *string  `json:"read_state,omitempty"`
	PublishStage *string  `json:"publish_stage,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Description  string   `json:"description,omitempty"`
	Author       string   `json:"author,omitempty"`
	Source       string   `json:"source,omitempty"`
}

func toDTO(e domain.Entry) entryDTO {
	d := entryDTO{
		ID:          e.ID(),
		HabrID:      e.HabrID(),
		Title:       e.Title(),
		URL:         e.URL(),
		Category:    e.Category().String(),
		Kind:        e.Kind(),
		Lifecycle:   e.Lifecycle().String(),
		Tags:        e.Tags(),
		Description: e.Description(),
		Author:      e.Author(),
		Source:      e.Source(),
	}
	if v := e.Verdict(); v != nil {
		s := v.String()
		d.Verdict = &s
	}
	if r := e.ReadState(); r != nil {
		s := r.String()
		d.ReadState = &s
	}
	if p := e.PublishStage(); p != nil {
		s := p.String()
		d.PublishStage = &s
	}
	return d
}
