// Package archmap reads architecture maps — hand-written documents that say how
// a project actually works, with every claim anchored to a file:line in live
// code — and hands them over in one shape.
//
// Одна форма и есть причина, по которой пакет существует. Карт две, написаны
// они в разных схемах: у карты движка есть коммит, к которому её можно
// привязать, и нет ни зон, ни находок; у карты рабочего места нет коммита
// (проект намеренно не под git), зато есть зоны, находки, покрытие с диска и
// раздел приёмки. Разбирать оба диалекта на странице значило бы держать
// правило соответствия там же, где рисуют, и однажды научить одну вкладку
// одному, а другую другому.
//
// Разделы, которых в файле нет, приезжают пустыми списками, а не отсутствием:
// «в карте этого раздела нет» и «раздел пуст» страница показывает одинаково
// честно, а вот null у .length однажды уронил дашборд целиком.
package archmap

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Map — карта архитектуры в той форме, в какой её видит страница.
type Map struct {
	// ID адресует карту в URL и в списке. Берётся из имени проекта, а не из
	// пути к файлу: путь — это машина владельца, и наружу он не уезжает.
	ID      string `json:"id"`
	Project string `json:"project"`
	// Commit — коммит кода, против которого карту сверяли. Пуст у проекта не
	// под git, и это не «неизвестно»: сверять там просто не с чем, свежесть
	// держится на пересчёте якорей.
	Commit    string `json:"commit,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
	// Note — что карта говорит о себе самой: project_note у одной схемы,
	// provenance.note у другой. По смыслу это одно и то же.
	Note string `json:"note,omitempty"`
	// Page — собственный разбор проекта: страница, написанная человеком, с
	// рисованными диаграммами и тем, чего механическая карта сказать не умеет —
	// что делалось по неделям, зачем каждая технология, где ошиблись. Путь ведёт
	// в базу знаний, витрина открывает его маршрутом /kb/.
	//
	// Пустая строка означает «разбора нет», и раздел тогда не показывается вовсе:
	// пустая вкладка читается как поломка, отсутствующая — как «здесь этого нет».
	Page string `json:"page,omitempty"`
	// Examples — чем карта иллюстрирует «не список модулей, а действия целиком».
	// Живут в карте, а не в вёрстке: у каждой карты они свои, а вкладка одна.
	Examples  []string `json:"examples"`
	Roots     []Root   `json:"roots"`
	RootsNote string   `json:"roots_note,omitempty"`

	Layers        []Layer   `json:"layers"`
	Zones         []Zone    `json:"zones"`
	Nodes         []Node    `json:"nodes"`
	Flows         []Flow    `json:"flows"`
	Findings      []Finding `json:"findings"`
	Gaps          []string  `json:"gaps"`
	RuntimeChecks []string  `json:"runtime_checks"`

	Coverage   *Coverage   `json:"coverage,omitempty"`
	Acceptance *Acceptance `json:"acceptance,omitempty"`

	Stats Stats `json:"stats"`
}

// Root — дерево, в котором лежит часть описанного. Якорь несёт имя дерева
// префиксом («work:digest/run.sh:12»), и без словаря корней развернуть его
// некуда.
type Root struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Layer — горизонт, на котором стоит узел: кто действует, поверхности,
// команды, домен, адаптеры, файлы.
type Layer struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Order int    `json:"order"`
}

// Zone — область приёмки: карту принимают частями, потому что целиком её не
// прочитывают за раз.
type Zone struct {
	Name string `json:"name"`
	// Accepted говорит только о том, что у зоны есть запись о приёмке. Карта
	// без раздела приёмки даёт false у всех зон, и это третий ответ — «сверять
	// не с чем», а не «не принята».
	Accepted bool   `json:"accepted"`
	Note     string `json:"note,omitempty"`
	Flows    int    `json:"flows"`
	Steps    int    `json:"steps"`
}

// Node — участник: команда, usecase, файл владельца, внешний сервис.
type Node struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Layer    string   `json:"layer,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Sources  []string `json:"sources"`
}

// Flow — сценарий: как работа проходит через участников от начала до конца.
type Flow struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	Zone    string `json:"zone,omitempty"`
	Steps   []Step `json:"steps"`
}

// Step — один переход со ссылкой на строку живого кода.
type Step struct {
	N      int    `json:"n"`
	From   string `json:"from"`
	To     string `json:"to"`
	Call   string `json:"call"`
	Detail string `json:"detail,omitempty"`
	Source string `json:"source,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	// Unverified — шаг, который не подтверждён ни прогоном, ни символом в коде.
	// Держится отдельным полем, чтобы страница могла назвать его вслух: карта,
	// молчащая о том, чего она не проверила, обещает больше, чем знает.
	Unverified bool   `json:"unverified"`
	Why        string `json:"why,omitempty"`
	// Branch — ветка, а не продолжение прямого пути.
	Branch bool `json:"branch"`
}

// Finding — что карта нашла, пока её писали, вместе с судьбой находки.
type Finding struct {
	ID       string `json:"id"`
	Zone     string `json:"zone,omitempty"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	Severity string `json:"severity,omitempty"`
	Status   string `json:"status,omitempty"`
	Fix      string `json:"fix,omitempty"`
}

// Coverage — область карты, взятая с диска, и то, что из неё исключено с
// причиной. Пока полнота была прозой, проза врала.
type Coverage struct {
	Scope      CoverageScope `json:"scope"`
	Exclusions []Exclusion   `json:"exclusions"`
	Note       string        `json:"note,omitempty"`
}

// CoverageScope — по какой маске область собирается.
type CoverageScope struct {
	Root        string   `json:"root,omitempty"`
	Patterns    []string `json:"patterns"`
	Also        []string `json:"also"`
	ExcludeDirs []string `json:"exclude_dirs"`
}

// Exclusion — файл вне карты и причина, по которой он вне. Исключение без
// причины — это дыра, а не исключение.
type Exclusion struct {
	Path string `json:"path"`
	Why  string `json:"why,omitempty"`
}

// Acceptance — приёмка смысла: то, чего механическая проверка спросить не
// умеет.
type Acceptance struct {
	ClassesRun []string `json:"classes_run"`
	NotDone    string   `json:"not_done,omitempty"`
	Note       string   `json:"note,omitempty"`
}

// Stats — объём карты, посчитанный по данным. Считается здесь, а не пишется в
// файл словами: на живой карте раздел прозы говорил «пять шагов unverified»
// при четырёх, и разошлись они именно потому, что число было записано рукой.
type Stats struct {
	Nodes         int `json:"nodes"`
	Flows         int `json:"flows"`
	Steps         int `json:"steps"`
	Unverified    int `json:"unverified"`
	Findings      int `json:"findings"`
	RuntimeChecks int `json:"runtime_checks"`
}

// rawMap — файл как он лежит на диске, оба диалекта сразу. Поля, которых в
// конкретной схеме нет, остаются нулевыми, и разбор об этом не спотыкается.
type rawMap struct {
	Project     string   `json:"project"`
	Commit      string   `json:"commit"`
	CheckedAt   string   `json:"checked_at"`
	ProjectNote string   `json:"project_note"`
	Page        string   `json:"page"`
	Examples    []string `json:"examples"`
	Provenance  struct {
		Note      string            `json:"note"`
		Roots     map[string]string `json:"roots"`
		RootsNote string            `json:"roots_note"`
	} `json:"provenance"`
	Layers        []Layer   `json:"layers"`
	Zones         []string  `json:"zones"`
	Nodes         []Node    `json:"nodes"`
	Flows         []Flow    `json:"flows"`
	Findings      []Finding `json:"findings"`
	Gaps          []string  `json:"gaps"`
	RuntimeChecks []string  `json:"runtime_checks"`
	Coverage      *Coverage `json:"coverage"`
	Acceptance    *struct {
		Zones      map[string]string `json:"zones"`
		ClassesRun []string          `json:"classes_run"`
		NotDone    string            `json:"not_done"`
		Note       string            `json:"note"`
	} `json:"acceptance"`
}

// Load reads one map file and normalises it.
//
// Ошибка всегда называет файл: карт подключают несколько, и «invalid character»
// без имени не говорит, какую из них чинить.
func Load(path string) (Map, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Map{}, fmt.Errorf("architecture map: %w", err)
	}
	var raw rawMap
	if err := json.Unmarshal(data, &raw); err != nil {
		return Map{}, fmt.Errorf("architecture map %s: %w", filepath.Base(path), err)
	}
	if strings.TrimSpace(raw.Project) == "" {
		return Map{}, fmt.Errorf("architecture map %s: field project is empty, and the map is addressed by it", filepath.Base(path))
	}
	id := slug(raw.Project)
	if id == "" {
		return Map{}, fmt.Errorf("architecture map %s: field project %q gives no address made of latin letters and digits", filepath.Base(path), raw.Project)
	}

	m := Map{
		ID:            id,
		Project:       raw.Project,
		Commit:        raw.Commit,
		CheckedAt:     raw.CheckedAt,
		Note:          firstNonEmpty(raw.ProjectNote, raw.Provenance.Note),
		Page:          raw.Page,
		Examples:      nonNil(raw.Examples),
		RootsNote:     raw.Provenance.RootsNote,
		Roots:         roots(raw.Provenance.Roots),
		Layers:        nonNil(raw.Layers),
		Nodes:         nonNil(raw.Nodes),
		Flows:         nonNil(raw.Flows),
		Findings:      nonNil(raw.Findings),
		Gaps:          nonNil(raw.Gaps),
		RuntimeChecks: nonNil(raw.RuntimeChecks),
		Coverage:      raw.Coverage,
	}
	for i := range m.Nodes {
		m.Nodes[i].Sources = nonNil(m.Nodes[i].Sources)
	}
	for i := range m.Flows {
		m.Flows[i].Steps = nonNil(m.Flows[i].Steps)
	}
	if raw.Acceptance != nil {
		m.Acceptance = &Acceptance{
			ClassesRun: nonNil(raw.Acceptance.ClassesRun),
			NotDone:    raw.Acceptance.NotDone,
			Note:       raw.Acceptance.Note,
		}
	}
	if m.Coverage != nil {
		m.Coverage.Exclusions = nonNil(m.Coverage.Exclusions)
		m.Coverage.Scope.Patterns = nonNil(m.Coverage.Scope.Patterns)
		m.Coverage.Scope.Also = nonNil(m.Coverage.Scope.Also)
		m.Coverage.Scope.ExcludeDirs = nonNil(m.Coverage.Scope.ExcludeDirs)
	}
	if err := checkAnchors(m, filepath.Base(path)); err != nil {
		return Map{}, err
	}

	accepted := map[string]string{}
	if raw.Acceptance != nil {
		accepted = raw.Acceptance.Zones
	}
	m.Zones = zones(raw.Zones, m.Flows, accepted)
	m.Stats = count(m)
	return m, nil
}

// LoadAll reads maps in the order given.
//
// Проверка на повтор адреса здесь, а не у вызывающего: две карты с одним
// адресом сделали бы вторую недостижимой, то есть потеряли бы целый документ
// молча — а молчаливая потеря хуже отказа стартовать.
func LoadAll(paths []string) ([]Map, error) {
	out := make([]Map, 0, len(paths))
	seen := map[string]string{}
	for _, p := range paths {
		m, err := Load(p)
		if err != nil {
			return nil, err
		}
		if first, dup := seen[m.ID]; dup {
			return nil, fmt.Errorf("architecture maps %s and %s share the address %q, and the second would be unreachable",
				filepath.Base(first), filepath.Base(p), m.ID)
		}
		seen[m.ID] = p
		out = append(out, m)
	}
	return out, nil
}

// checkAnchors ловит шаг, ведущий к узлу, которого в карте нет. Такой шаг
// рисуется стрелкой в пустоту, и страница показала бы её как настоящую связь.
func checkAnchors(m Map, file string) error {
	known := make(map[string]bool, len(m.Nodes))
	for _, n := range m.Nodes {
		known[n.ID] = true
	}
	for _, f := range m.Flows {
		for _, s := range f.Steps {
			for _, ref := range []string{s.From, s.To} {
				if ref != "" && !known[ref] {
					return fmt.Errorf("architecture map %s: flow %q step %d points at node %q, which the map does not describe",
						file, f.ID, s.N, ref)
				}
			}
		}
	}
	return nil
}

// zones сводит три источника знания о зоне: явный список, сценарии и приёмку.
//
// Явный список главнее сценариев намеренно. Зона без единого сценария всё
// равно существует, и выбросить её значило бы скрыть ровно то место, которое
// никем не описано.
func zones(declared []string, flows []Flow, accepted map[string]string) []Zone {
	order := slices.Clone(declared)
	for _, f := range flows {
		if f.Zone != "" && !slices.Contains(order, f.Zone) {
			order = append(order, f.Zone)
		}
	}
	out := make([]Zone, 0, len(order))
	for _, name := range order {
		z := Zone{Name: name}
		if note, ok := accepted[name]; ok {
			z.Accepted, z.Note = true, note
		}
		for _, f := range flows {
			if f.Zone != name {
				continue
			}
			z.Flows++
			z.Steps += len(f.Steps)
		}
		out = append(out, z)
	}
	return out
}

func count(m Map) Stats {
	s := Stats{
		Nodes:         len(m.Nodes),
		Flows:         len(m.Flows),
		Findings:      len(m.Findings),
		RuntimeChecks: len(m.RuntimeChecks),
	}
	for _, f := range m.Flows {
		s.Steps += len(f.Steps)
		for _, st := range f.Steps {
			if st.Unverified {
				s.Unverified++
			}
		}
	}
	return s
}

func roots(m map[string]string) []Root {
	out := make([]Root, 0, len(m))
	for _, name := range slices.Sorted(maps.Keys(m)) {
		out = append(out, Root{Name: name, Path: m[name]})
	}
	return out
}

// slug делает из имени проекта адрес: строчные латинские буквы, цифры и дефис.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ' || r == '.':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// nonNil отдаёт пустой список вместо nil: nil Go пишет как null, а страница
// зовёт у ответа .length.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
