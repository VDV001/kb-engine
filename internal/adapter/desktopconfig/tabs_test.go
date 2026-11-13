package desktopconfig_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Без записи в конфиге — прежний набор окна плюс картинки: человек, который
// вкладки не трогал, видит то же, что раньше.
func TestConfig_TabListDefault(t *testing.T) {
	want := []desktopconfig.Tab{
		{Name: "База знаний", Kind: desktopconfig.TabByType, Value: "kb"},
		{Name: "Документы", Kind: desktopconfig.TabByType, Value: "doc"},
		{Name: "Код", Kind: desktopconfig.TabByType, Value: "code"},
		{Name: "Картинки", Kind: desktopconfig.TabByType, Value: "image"},
		{Name: "Артефакты", Kind: desktopconfig.TabView, Value: "artifacts"},
	}
	if got := (desktopconfig.Config{}).TabList(); !slices.Equal(got, want) {
		t.Fatalf("TabList() = %+v, want %+v", got, want)
	}
	// Пустой, но заданный список — выбор человека: только «Все».
	if got := (desktopconfig.Config{Tabs: []desktopconfig.Tab{}}).TabList(); len(got) != 0 {
		t.Fatalf("пустой список = %+v", got)
	}
}

func TestTab_Check(t *testing.T) {
	tests := []struct {
		name string
		tab  desktopconfig.Tab
		want error
	}{
		{"тип док", desktopconfig.Tab{Name: "Доки", Kind: desktopconfig.TabByType, Value: "doc"}, nil},
		{"тип картинки", desktopconfig.Tab{Name: "Img", Kind: desktopconfig.TabByType, Value: "image"}, nil},
		{"источник", desktopconfig.Tab{Name: "Работа", Kind: desktopconfig.TabBySource, Value: "folder-1"}, nil},
		{"без имени", desktopconfig.Tab{Kind: desktopconfig.TabByType, Value: "doc"}, desktopconfig.ErrTab},
		{"неизвестный тип", desktopconfig.Tab{Name: "X", Kind: desktopconfig.TabByType, Value: "video"}, desktopconfig.ErrTab},
		{"источник без id", desktopconfig.Tab{Name: "X", Kind: desktopconfig.TabBySource}, desktopconfig.ErrTab},
		{"неизвестный вид", desktopconfig.Tab{Name: "X", Kind: "tag", Value: "go"}, desktopconfig.ErrTab},
		{"экран артефакты", desktopconfig.Tab{Name: "Артефакты", Kind: desktopconfig.TabView, Value: "artifacts"}, nil},
		{"неизвестный экран", desktopconfig.Tab{Name: "X", Kind: desktopconfig.TabView, Value: "chat"}, desktopconfig.ErrTab},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.tab.Check()
			if tt.want == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// Вкладки переживают запись и чтение; негодная запись отбрасывается при
// чтении, а не роняет конфиг — его читает и kbengine-mcp.
func TestRead_TabsRoundTripAndBadDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	tabs := []desktopconfig.Tab{
		{Name: "Работа", Kind: desktopconfig.TabBySource, Value: "folder-1"},
		{Name: "Код", Kind: desktopconfig.TabByType, Value: "code"},
	}
	if err := desktopconfig.Write(path, desktopconfig.Config{Tabs: tabs}); err != nil {
		t.Fatal(err)
	}
	got, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Tabs, tabs) {
		t.Fatalf("tabs = %+v, want %+v", got.Tabs, tabs)
	}
	// Без имени или значения — отбрасывается; неизвестный вид или тип (его
	// записала более новая сборка) хранится, как неизвестный модуль (ревью #431).
	path = writeRaw(t, `{"sources":[],"tabs":[{"name":"Код","kind":"type","value":"code"},{"name":"","kind":"type","value":"doc"},{"name":"X","kind":"tag","value":"go"},{"name":"Видео","kind":"type","value":"video"},{"name":"Y","kind":"source","value":""}]}`)
	got, err = desktopconfig.Read(path)
	if err != nil {
		t.Fatalf("err = %v: вкладки не должны ронять чтение", err)
	}
	want := []desktopconfig.Tab{
		{Name: "Код", Kind: desktopconfig.TabByType, Value: "code"},
		{Name: "X", Kind: "tag", Value: "go"},
		{Name: "Видео", Kind: desktopconfig.TabByType, Value: "video"},
		{Name: "Артефакты", Kind: desktopconfig.TabView, Value: "artifacts"}, // прежняя запись: «Артефакты» были всегда
	}
	if !slices.Equal(got.Tabs, want) {
		t.Fatalf("tabs = %+v, want %+v", got.Tabs, want)
	}
}

// «Только Все» (пустой список) переживает запись: иначе следующее чтение
// вернуло бы набор по умолчанию вопреки выбору человека.
func TestWrite_EmptyTabsStayEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := desktopconfig.Write(path, desktopconfig.Config{Tabs: []desktopconfig.Tab{}}); err != nil {
		t.Fatal(err)
	}
	got, err := desktopconfig.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tabs == nil || len(got.TabList()) != 0 {
		t.Fatalf("tabs = %#v: пустой выбор стал набором по умолчанию", got.Tabs)
	}
}

// «Артефакты» стали вкладкой списка (решение владельца 13.11): у прежней
// записи они дописываются один раз — раньше они были всегда; убранные
// человеком после этого не возвращаются.
func TestRead_ArtifactsTabAddedOnce(t *testing.T) {
	art := desktopconfig.Tab{Name: "Артефакты", Kind: desktopconfig.TabView, Value: "artifacts"}
	code := desktopconfig.Tab{Name: "Код", Kind: desktopconfig.TabByType, Value: "code"}
	tests := []struct {
		name, raw string
		want      []desktopconfig.Tab
	}{
		{"прежняя запись — дописаны в конец", `{"sources":[],"tabs":[{"name":"Код","kind":"type","value":"code"}]}`, []desktopconfig.Tab{code, art}},
		{"прежнее «только Все» — тоже: артефакты тогда были видны", `{"sources":[],"tabs":[]}`, []desktopconfig.Tab{art}},
		{"уже стоят — не повторяются", `{"sources":[],"tabs":[{"name":"Мои","kind":"view","value":"artifacts"},{"name":"Код","kind":"type","value":"code"}]}`,
			[]desktopconfig.Tab{{Name: "Мои", Kind: desktopconfig.TabView, Value: "artifacts"}, code}},
		{"убраны человеком в новой сборке — не возвращаются", `{"sources":[],"tabsV":1,"tabs":[{"name":"Код","kind":"type","value":"code"}]}`, []desktopconfig.Tab{code}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := desktopconfig.Read(writeRaw(t, tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.TabList(), tt.want) {
				t.Errorf("tabs = %+v, want %+v", got.TabList(), tt.want)
			}
		})
	}
	// Набор, уже решенный в этой сборке (tabsV), переживает запись и чтение без «Артефактов».
	path := filepath.Join(t.TempDir(), "config.json")
	if err := desktopconfig.Write(path, desktopconfig.Config{Tabs: []desktopconfig.Tab{code}, TabsV: 1}); err != nil {
		t.Fatal(err)
	}
	if got, err := desktopconfig.Read(path); err != nil || !slices.Equal(got.TabList(), []desktopconfig.Tab{code}) {
		t.Errorf("after write: tabs = %+v, err %v", got.TabList(), err)
	}
	// Прежняя запись, дошедшая до Write мимо Read, переезжает там же, а не
	// помечается решенной без «Артефактов» (ревью #596).
	if err := desktopconfig.Write(path, desktopconfig.Config{Tabs: []desktopconfig.Tab{code}}); err != nil {
		t.Fatal(err)
	}
	if got, err := desktopconfig.Read(path); err != nil || !slices.Equal(got.TabList(), []desktopconfig.Tab{code, art}) {
		t.Errorf("old config through Write: tabs = %+v, err %v", got.TabList(), err)
	}
}
