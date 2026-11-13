package desktopconfig

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// TabKind — по чему вкладка поиска отбирает выдачу (решение владельца
// 05.11.2026: нужны оба вида, набор правится в настройках).
type TabKind string

const (
	// TabByType — по типу: записи базы (kb) или файлы вида doc, code, image.
	TabByType TabKind = "type"
	// TabBySource — по источнику: Value — id источника из Sources.
	TabBySource TabKind = "source"
	// TabView — экран окна среди вкладок поиска: Value — его имя (artifacts).
	TabView TabKind = "view"
)

// ErrTab — у вкладки нет имени, неизвестен вид или тип, нет id источника.
var ErrTab = errors.New("вкладка задана не полностью")

// tabTypes — типы, которые окно умеет отобрать: kb — записи базы, остальные —
// виды файлов из домена («прочее» вкладкой не бывает).
var tabTypes = []string{"kb", domain.FileKindDoc.String(), domain.FileKindCode.String(), domain.FileKindImage.String()}

// tabViews — экраны окна, которые стоят среди вкладок поиска.
var tabViews = []string{"artifacts"}

// artifactsTab — «Артефакты» (#547): до 13.11.2026 стояли при вкладках всегда,
// теперь — вкладка списка, ее двигают и убирают (решение владельца 13.11).
var artifactsTab = Tab{Name: "Артефакты", Kind: TabView, Value: "artifacts"}

// TabsVersion — версия набора вкладок: 1 — «Артефакты» уже в списке. У записи
// без нее (прежняя сборка) «Артефакты» дописываются один раз.
const TabsVersion = 1

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
	case TabView:
		if !slices.Contains(tabViews, t.Value) {
			return fmt.Errorf("%w: экран %q", ErrTab, t.Value)
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
		artifactsTab,
	}
}

// addArtifactsTab дописывает «Артефакты» в конец набора прежней сборки: тогда
// они были видны всегда, и пропасть после обновления не должны. Убранные
// человеком в этой сборке не возвращаются — набор уже помечен версией.
// ponytail: память о «убраны» — только tabsV; сборка старше 13.11, сохранив
// config.json, это поле теряет, и «Артефакты» вернутся — их снова уберут ×.
// Потолок — две сборки окна по очереди на одном Mac; путь — держать в Tab
// признак «убрана», если такое начнут делать.
func (c *Config) addArtifactsTab() {
	if c.Tabs != nil && c.TabsV < TabsVersion && !slices.ContainsFunc(c.Tabs, func(t Tab) bool { return t.Key() == artifactsTab.Key() }) {
		c.Tabs = append(c.Tabs, artifactsTab)
	}
	c.TabsV = TabsVersion
}

// TabList returns the tabs after «Все»: заданные, а без записи — DefaultTabs.
// Пустой заданный список — выбор человека (только «Все»), а не «не задано».
func (c Config) TabList() []Tab {
	if c.Tabs == nil {
		return DefaultTabs()
	}
	return c.Tabs
}

// Key — чем окно различает вкладки: вид и значение. Номер в списке не годится —
// после удаления или перестановки активной стала бы соседняя.
func (t Tab) Key() string { return string(t.Kind) + ":" + t.Value }

// dropBadTabs отбрасывает испорченные вкладки (без имени или значения), как
// dropBadModules: вкладка — только вид выдачи и не должна гасить источники
// MCP. Неизвестный вид или тип — не порча: его записала более новая сборка,
// он хранится, окно его не показывает.
func (c *Config) dropBadTabs() {
	if c.Tabs == nil {
		return
	}
	c.Tabs = slices.DeleteFunc(c.Tabs, func(t Tab) bool { return strings.TrimSpace(t.Name) == "" || t.Value == "" })
}
