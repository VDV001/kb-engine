package main

import (
	"errors"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Оформление окна сохраняется в config.json без переиндексации и видно
// снимку; по умолчанию — твой выбор без выключенных, то есть все акценты.
// «Все» и «Ничего» не стирают твой набор: к нему можно вернуться.
func TestSetAccents_SavesWithoutReindex(t *testing.T) {
	s, applied, _ := newSourcesSettings(t)
	if got := s.Accents(); got.Mode != desktopconfig.AccentsMine || len(got.Off) != 0 {
		t.Fatalf("Accents() по умолчанию = %+v", got)
	}
	calls := applied.calls
	next := desktopconfig.Accents{Mode: desktopconfig.AccentsAll, Off: []string{"pulse", "tip"}}
	if err := s.SetAccents(next); err != nil {
		t.Fatal(err)
	}
	if applied.calls != calls {
		t.Error("оформление вызвало переиндексацию")
	}
	cfg, err := desktopconfig.Read(s.path)
	if err != nil {
		t.Fatal(err)
	}
	same := func(a desktopconfig.Accents) bool { return a.Mode == next.Mode && slices.Equal(a.Off, next.Off) }
	if !same(cfg.Accents) || !same(s.Accents()) {
		t.Fatalf("в файле %+v, Accents() %+v", cfg.Accents, s.Accents())
	}
	if !same(s.snap.Load().cfg.Accents) {
		t.Error("снимок настроек не обновлен")
	}
}

// Негодное оформление — отказ, файл прежний.
func TestSetAccents_Rejects(t *testing.T) {
	s, _, _ := newSourcesSettings(t)
	if err := s.SetAccents(desktopconfig.Accents{Off: []string{"pulse", "pulse"}}); !errors.Is(err, desktopconfig.ErrAccent) {
		t.Fatalf("err = %v, want ErrAccent", err)
	}
	if cfg, _ := desktopconfig.Read(s.path); cfg.Accents.Mode != "" || cfg.Accents.Off != nil {
		t.Errorf("записано %+v", cfg.Accents)
	}
}
