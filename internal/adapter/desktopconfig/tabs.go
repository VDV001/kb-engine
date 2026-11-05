package desktopconfig

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// TabKind — по чему вкладка поиска отбирает выдачу (решение владельца
// 05.11.2026: нужны оба вида, набор правится в настройках).
type TabKind string

const (
	// TabByType — по типу: записи базы (kb) или файлы вида doc, code, image.
	TabByType TabKind = "type"
	// TabBySource — по источнику: Value — id источника из Sources.
	TabBySource TabKind = "source"
)

// ErrTab — у вкладки нет имени, неизвестен вид или тип, нет id источника.
var ErrTab = errors.New("вкладка задана не полностью")

// tabTypes — типы, которые окно умеет отобрать: kb — записи базы, остальные —
// виды файлов domain.FileKind.
var tabTypes = []string{"kb", "doc", "code", "image"}

// Tab — вкладка поиска окна. «Все» в списке не хранится: она всегда первая.
type Tab struct {
	Name  string  `json:"name"`
	Kind  TabKind `json:"kind"`
	Value string  `json:"value"`
}

// Check validates one tab.
func (t Tab) Check() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("%w: без имени", ErrTab)
	}
	switch t.Kind {
	case TabByType:
		if !slices.Contains(tabTypes, t.Value) {
			return fmt.Errorf("%w: тип %q", ErrTab, t.Value)
		}
	case TabBySource:
		if t.Value == "" {
			return fmt.Errorf("%w: %s без источника", ErrTab, t.Name)
		}
	default:
		return fmt.Errorf("%w: вид %q", ErrTab, t.Kind)
	}
	return nil
}

// DefaultTabs — набор, когда человек вкладки не настраивал: прежние вкладки
// окна и картинки, у которых тип в движке был, а вкладки не было.
func DefaultTabs() []Tab {
	return []Tab{
		{Name: "База знаний", Kind: TabByType, Value: "kb"},
		{Name: "Документы", Kind: TabByType, Value: "doc"},
		{Name: "Код", Kind: TabByType, Value: "code"},
		{Name: "Картинки", Kind: TabByType, Value: "image"},
	}
}

// TabList returns the tabs after «Все»: заданные, а без записи — DefaultTabs.
// Пустой заданный список — выбор человека (только «Все»), а не «не задано».
func (c Config) TabList() []Tab {
	if c.Tabs == nil {
		return DefaultTabs()
	}
	return c.Tabs
}

// dropBadTabs отбрасывает негодные вкладки, как dropBadModules: вкладка —
// только вид выдачи, и испорченная запись не должна гасить источники MCP.
func (c *Config) dropBadTabs() {
	if c.Tabs == nil {
		return
	}
	c.Tabs = slices.DeleteFunc(c.Tabs, func(t Tab) bool { return t.Check() != nil })
}
