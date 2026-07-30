// Package analyticsconfig loads the curated semantic layer of the dashboard —
// patterns, gaps, contradictions and manifesto quotes — from analytics_config.
// json. The content is data (the engine renders whatever config it is given);
// only the known fields are modelled, extras are ignored.
package analyticsconfig

import (
	"encoding/json"
	"fmt"
	"os"
)

// Pattern is a recurring theme observed across catalog clusters.
type Pattern struct {
	Name     string   `json:"name"`
	Clusters []string `json:"clusters"`
	Desc     string   `json:"desc"`
}

// Gap is an under-covered topic.
type Gap struct {
	Topic    string   `json:"topic"`
	Clusters []string `json:"clusters"`
	Priority string   `json:"priority"`
}

// Contradiction is a tension between two positions and its resolution.
type Contradiction struct {
	Title      string `json:"title"`
	A          string `json:"a"`
	B          string `json:"b"`
	Resolution string `json:"resolution"`
}

// Support is one confirmation under a manifesto thesis. The file writes it in
// two shapes — a plain sentence, or a structured catalog reference — and the
// view renders both, so both are modelled.
type Support struct {
	Text      string `json:"text,omitempty"`
	CatalogID int    `json:"catalog_id,omitempty"`
	Title     string `json:"title,omitempty"`
	Insight   string `json:"insight,omitempty"`
}

// UnmarshalJSON accepts either shape. A string becomes Text; an object fills
// the reference fields.
func (s *Support) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		*s = Support{Text: text}
		return nil
	}
	type object Support // без метода, чтобы не зациклиться
	var o object
	if err := json.Unmarshal(raw, &o); err != nil {
		return err
	}
	*s = Support(o)
	return nil
}

// ChainLink is one step of «откуда это следует»: a cluster and what it
// contributes to the meta-conclusion.
type ChainLink struct {
	Cluster  string `json:"cluster"`
	Evidence string `json:"evidence"`
}

// ManifestoQuote is a headline synthesis quote with provenance.
type ManifestoQuote struct {
	Quote    string    `json:"quote"`
	Source   string    `json:"source"`
	Date     string    `json:"date"`
	Type     string    `json:"type"`
	Weight   string    `json:"weight"`
	Supports []Support `json:"supports,omitempty"`
}

// Config is the modelled subset of analytics_config.json.
type Config struct {
	PullQuote       string           `json:"pull_quote,omitempty"`
	PullQuoteMeta   string           `json:"pull_quote_meta,omitempty"`
	InferenceChain  []ChainLink      `json:"inference_chain,omitempty"`
	Patterns        []Pattern        `json:"patterns"`
	Gaps            []Gap            `json:"gaps"`
	Contradictions  []Contradiction  `json:"contradictions"`
	ManifestoQuotes []ManifestoQuote `json:"manifesto_quotes"`
}

// Load reads and decodes the analytics config at path. Unknown fields are
// ignored so the file can carry more than the dashboard renders.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read analytics config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse analytics config: %w", err)
	}
	return cfg, nil
}
