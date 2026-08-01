// Package analyticsconfig loads the curated semantic layer of the dashboard —
// patterns, gaps, contradictions and manifesto quotes — from analytics_config.
// json. The content is data (the engine renders whatever config it is given);
// only the known fields are modelled, extras are ignored.
package analyticsconfig

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/daniil/kb-engine/internal/usecase/analytics"
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

	// Правый столбец аналитики. Данные лежали в файле и до сих пор, но их
	// обрезала эта структура: вид показывал под выводом число опор и не мог
	// показать сами опоры.
	PullQuoteSupports       []QuoteSupport `json:"pull_quote_supports,omitempty"`
	ContradictionResolution string         `json:"contradiction_resolution,omitempty"`
	// Разбиение категорий по тому, что с ними делает AI. Это ядро первого
	// манифестного тезиса, и нигде больше в движке его нет.
	// Graph is the owner's own topology: which categories he considers linked
	// and what he calls the link. The computed graph knows the first, never the
	// second.
	Graph []analytics.CuratedLink `json:"graph,omitempty"`

	AmplifyClusters []string `json:"amplify_clusters,omitempty"`
	ReplaceClusters []string `json:"replace_clusters,omitempty"`
	NeutralClusters []string `json:"neutral_clusters,omitempty"`
}

// QuoteSupport is one claim the pull quote rests on, and the cluster it came
// from. A count of supports without the supports themselves is a promise the
// reader cannot check.
type QuoteSupport struct {
	Cluster string `json:"cluster"`
	Claim   string `json:"claim"`
}

// UnmarshalJSON accepts both shapes the config uses: an object with a cluster
// and a claim, and a bare string — a quote with its attribution, which belongs
// to no cluster. On the live config that is 50 objects against 19 strings, and
// rewriting the owner's file for uniformity costs more than reading both.
func (q *QuoteSupport) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		q.Cluster, q.Claim = "", s
		return nil
	}
	// Псевдоним разрывает рекурсию: без него json снова позвал бы этот метод.
	type plain QuoteSupport
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("quote support: %w", err)
	}
	*q = QuoteSupport(p)
	return nil
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
