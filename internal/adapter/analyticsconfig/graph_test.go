package analyticsconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
)

// The config's graph block held 32 labelled connections that this loader
// dropped on the floor: the struct simply had no field for them.
func TestDecode_readsCuratedGraph(t *testing.T) {
	const doc = `{
	  "patterns": [], "gaps": [], "contradictions": [], "manifesto_quotes": [],
	  "graph": [
	    {"from": "claude-ecosystem", "to": "ai-agents-tools", "label": "MCP/протоколы"},
	    {"from": "devops", "to": "data-science", "label": "MLOps"}
	  ]
	}`

	path := filepath.Join(t.TempDir(), "analytics_config.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := analyticsconfig.Load(path)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(cfg.Graph) != 2 {
		t.Fatalf("Graph has %d links, want 2", len(cfg.Graph))
	}
	if cfg.Graph[0].Label != "MCP/протоколы" {
		t.Errorf("first label = %q", cfg.Graph[0].Label)
	}
	if cfg.Graph[0].From != "claude-ecosystem" || cfg.Graph[0].To != "ai-agents-tools" {
		t.Errorf("first link = %s→%s", cfg.Graph[0].From, cfg.Graph[0].To)
	}
}
