package analyticsconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
)

const sample = `{
  "_comment": "ignored",
  "pull_quote": "ignored too",
  "patterns": [
    {"name": "Verification > Generation", "clusters": ["ai-agents-tools", "dev-practices"], "desc": "Верификация важнее"}
  ],
  "gaps": [
    {"topic": "Testing strategy с AI", "clusters": ["golang"], "priority": "low"}
  ],
  "contradictions": [
    {"title": "Будущее профессии", "a": "Исчезнет", "b": "Трансформируется", "resolution": "Фильтрация по глубине"}
  ],
  "manifesto_quotes": [
    {"quote": "AI — мультипликатор экспертизы", "source": "KB Даниила", "date": "апрель 2026", "type": "synthesis", "weight": "primary", "supports": ["503+ статей", {"shape": "object, not string"}]}
  ],
  "graph": [{"unmodelled": true}]
}`

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "analytics_config.json")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg, err := analyticsconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Patterns) != 1 || cfg.Patterns[0].Name != "Verification > Generation" {
		t.Errorf("patterns = %+v", cfg.Patterns)
	}
	if len(cfg.Patterns[0].Clusters) != 2 {
		t.Errorf("pattern clusters = %v, want 2", cfg.Patterns[0].Clusters)
	}
	if len(cfg.Gaps) != 1 || cfg.Gaps[0].Priority != "low" {
		t.Errorf("gaps = %+v", cfg.Gaps)
	}
	if len(cfg.Contradictions) != 1 || cfg.Contradictions[0].Resolution != "Фильтрация по глубине" {
		t.Errorf("contradictions = %+v", cfg.Contradictions)
	}
	if len(cfg.ManifestoQuotes) != 1 || cfg.ManifestoQuotes[0].Weight != "primary" {
		t.Errorf("quotes = %+v", cfg.ManifestoQuotes)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := analyticsconfig.Load("/no/such/config.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}
