package desktopconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Испорченный лимит или своя цена в config.json не мешают окну открыться:
// лимит снимается, цена отбрасывается, исправная остается.
func TestRead_dropsBadSpend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"sources":[],"spend":{"limit":"много","prices":{"ok":{"in":"0,1","out":"0.5"},"bad":{"in":"-1","out":"0"},"":{"in":"1","out":"1"}}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Spend.Limit != "" || len(cfg.Spend.Prices) != 1 || cfg.Spend.Prices["ok"].In != "0,1" {
		t.Fatalf("spend = %+v", cfg.Spend)
	}
}
