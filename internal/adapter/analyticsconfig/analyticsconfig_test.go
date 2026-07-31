package analyticsconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
)

const sample = `{
  "_comment": "ignored",
  "pull_quote": "AI — мультипликатор экспертизы",
  "pull_quote_meta": "Вывод мета-анализа · июль 2026",
  "inference_chain": [
    {"cluster": "vibe-coding", "evidence": "архитектурный вкус решает"}
  ],
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
    {"quote": "AI — мультипликатор экспертизы", "source": "KB Даниила", "date": "апрель 2026", "type": "synthesis", "weight": "primary", "supports": ["503+ статей", {"catalog_id": 897, "title": "NLA", "insight": "operational definition"}]}
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

	// The full analytics view renders these; a subset that drops them renders
	// an empty right rail while the file plainly carries the content.
	if cfg.PullQuote != "AI — мультипликатор экспертизы" || cfg.PullQuoteMeta == "" {
		t.Errorf("pull quote = %q / %q", cfg.PullQuote, cfg.PullQuoteMeta)
	}
	if len(cfg.InferenceChain) != 1 || cfg.InferenceChain[0].Cluster != "vibe-coding" {
		t.Errorf("inference chain = %+v", cfg.InferenceChain)
	}

	// Supports come in two shapes in the real file — a plain sentence and a
	// structured catalog reference — and the manifesto cards render both.
	sup := cfg.ManifestoQuotes[0].Supports
	if len(sup) != 2 || sup[0].Text != "503+ статей" {
		t.Fatalf("supports = %+v", sup)
	}
	if sup[1].CatalogID != 897 || sup[1].Title != "NLA" || sup[1].Insight != "operational definition" {
		t.Errorf("structured support = %+v", sup[1])
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := analyticsconfig.Load("/no/such/config.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// Правый столбец аналитики в исходном дашборде несёт четыре блока, которых в
// движке не было: опоры под выводом, разрешение противоречия и разбиение
// кластеров на «усиливает / заменяет / нейтрально». Данные лежали в файле всё
// это время — их обрезала структура, а не отсутствие.
//
// «Рост за 12 недель» и «центральные узлы графа» сюда намеренно не входят:
// первое уже есть на дашборде лентой за 21 неделю, второе — самим графом.
func TestLoad_sidebarBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	src := `{
	  "pull_quote": "тезис",
	  "pull_quote_supports": [{"cluster": "vibe-coding", "claim": "вкус решает"}],
	  "contradiction_resolution": "усиливает 16 против заменяет 2",
	  "amplify_clusters": ["claude-ecosystem", "devops"],
	  "replace_clusters": ["services-reference"],
	  "neutral_clusters": ["meta"],
	  "patterns": [], "gaps": [], "contradictions": [], "manifesto_quotes": []
	}`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := analyticsconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.PullQuoteSupports) != 1 || cfg.PullQuoteSupports[0].Cluster != "vibe-coding" {
		t.Errorf("PullQuoteSupports = %+v", cfg.PullQuoteSupports)
	}
	if cfg.ContradictionResolution != "усиливает 16 против заменяет 2" {
		t.Errorf("ContradictionResolution = %q", cfg.ContradictionResolution)
	}
	if len(cfg.AmplifyClusters) != 2 || len(cfg.ReplaceClusters) != 1 || len(cfg.NeutralClusters) != 1 {
		t.Errorf("направления: %v / %v / %v",
			cfg.AmplifyClusters, cfg.ReplaceClusters, cfg.NeutralClusters)
	}
}
