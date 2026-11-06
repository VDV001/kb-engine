package main

import (
	"errors"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Оформление окна сохраняется в config.json без переиндексации и видно
// снимку; по умолчанию выключенных нет — все акценты спектра на месте.
func TestSetAccentsOff_SavesWithoutReindex(t *testing.T) {
	s, applied, _ := newSourcesSettings(t)
	if got := s.AccentsOff(); len(got) != 0 {
		t.Fatalf("AccentsOff() по умолчанию = %v", got)
	}
	calls := applied.calls
	off := []string{"pulse", "tip"}
	if err := s.SetAccentsOff(off); err != nil {
		t.Fatal(err)
	}
	if applied.calls != calls {
		t.Error("оформление вызвало переиндексацию")
	}
	cfg, err := desktopconfig.Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.AccentsOff, off) || !slices.Equal(s.AccentsOff(), off) {
		t.Fatalf("в файле %v, AccentsOff() %v", cfg.AccentsOff, s.AccentsOff())
	}
	if !slices.Equal(s.snap.Load().cfg.AccentsOff, off) {
		t.Error("снимок настроек не обновлен")
	}
	// Пустой список — снова все включены.
	if err := s.SetAccentsOff(nil); err != nil {
		t.Fatal(err)
	}
	if got := s.AccentsOff(); len(got) != 0 {
		t.Fatalf("AccentsOff() после сброса = %v", got)
	}
}

// Негодный id — отказ, файл прежний.
func TestSetAccentsOff_Rejects(t *testing.T) {
	s, _, _ := newSourcesSettings(t)
	if err := s.SetAccentsOff([]string{"pulse", "pulse"}); !errors.Is(err, desktopconfig.ErrAccent) {
		t.Fatalf("err = %v, want ErrAccent", err)
	}
	if cfg, _ := desktopconfig.Read(s.path); cfg.AccentsOff != nil {
		t.Errorf("записано %v", cfg.AccentsOff)
	}
}
