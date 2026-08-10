package archmap_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/archmap"
)

// Две живые карты написаны в РАЗНЫХ схемах: у карты движка есть commit и
// project_note, у карты рабочего места — zones, findings, coverage, acceptance
// и три корня вместо коммита. Адаптер существует затем, чтобы страница видела
// одну форму, а не разбирала два диалекта у себя.
const engineMap = `{
  "project": "test-engine",
  "commit": "abc1234",
  "project_note": "числа о содержимом личной базы убраны намеренно",
  "layers": [
    {"id": "commands", "title": "Команды", "order": 3},
    {"id": "domain", "title": "Домен", "order": 5}
  ],
  "nodes": [
    {"id": "cmd-fin", "title": "fin", "subtitle": "деньги", "layer": "commands", "kind": "service", "sources": ["cmd/kbengine/fin.go:123"]},
    {"id": "dom-money", "title": "Money", "layer": "domain", "kind": "data", "sources": []}
  ],
  "flows": [
    {"id": "expense", "title": "Трата", "summary": "команда → журнал", "zone": "Деньги",
     "steps": [
       {"n": 1, "from": "cmd-fin", "to": "dom-money", "call": "ParseMoney", "detail": "разбор суммы", "source": "internal/domain/money.go:125", "unverified": false},
       {"n": 2, "from": "dom-money", "to": "cmd-fin", "call": "Add", "detail": "запись", "source": "internal/usecase/finance/finance.go:108", "unverified": true, "why": "живьём не прогонялось"}
     ]},
    {"id": "serve", "title": "Витрина", "summary": "флаги → маршруты", "zone": "Витрина",
     "steps": [
       {"n": 1, "from": "cmd-fin", "to": "dom-money", "call": "NewServer", "detail": "маршруты", "source": "internal/adapter/httpapi/server.go:145", "unverified": false, "branch": true}
     ]}
  ],
  "gaps": ["гейты в scripts/gates в карту не вошли намеренно"],
  "runtime_checks": ["fin add на временном журнале: повтор отвергнут"]
}`

const coworkMap = `{
  "project": "test-workspace",
  "checked_at": "2026-08-08",
  "provenance": {
    "note": "рабочее место не под git, коммита у карты нет",
    "roots": {"work": "~/work", "dotfiles": "~/dotfiles"},
    "roots_note": "карта, ограниченная одним деревом, описывала бы треть машины"
  },
  "layers": [{"id": "jobs", "title": "Задания", "order": 1}],
  "zones": ["Автоматизация", "Память"],
  "nodes": [
    {"id": "digest", "title": "Дайджест", "layer": "jobs", "kind": "job", "sources": ["work:digest/run.sh:12"]}
  ],
  "flows": [
    {"id": "collect", "title": "Сбор", "summary": "rss → кандидаты", "zone": "Автоматизация",
     "steps": [{"n": 1, "from": "digest", "to": "digest", "call": "collect.py", "detail": "сбор", "source": "work:digest/collect.py:40", "unverified": false}]}
  ],
  "findings": [
    {"id": "f-orphan", "zone": "Автоматизация", "title": "Сторож написан, но его никто не зовёт",
     "detail": "задания в launchd нет", "evidence": "work:digest/audit_watch.sh:38",
     "severity": "high", "status": "починено", "fix": "заведён plist на 21:00"}
  ],
  "gaps": ["код движка в область карты не входит"],
  "runtime_checks": ["сбор прогнан живьём: 42 кандидата, ошибок нет"],
  "coverage": {
    "scope": {"root": "work", "patterns": ["*.sh", "*.py"], "also": [".claude/commands/*.md"], "exclude_dirs": [".venv", "node_modules"]},
    "exclusions": [{"path": "_setup/stop-hook.py", "why": "черновик хука, не подключён ничем"}],
    "note": "область берётся с диска, а не из списка в карте"
  },
  "acceptance": {
    "zones": {"Автоматизация": "принята: 1 сценарий, найден 1 дефект", "Память": "принята: сверено с кодом"},
    "classes_run": ["дубли заголовков сценариев — 0"],
    "not_done": "зона «Совет» живьём не прогонялась",
    "note": "приёмка нужна для того, чего валидатор спросить не умеет"
  }
}`

func writeMap(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func loadOne(t *testing.T, body string) archmap.Map {
	t.Helper()
	m, err := archmap.Load(writeMap(t, "map.json", body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return m
}

func TestLoad_engineSchema(t *testing.T) {
	m := loadOne(t, engineMap)

	if m.ID != "test-engine" {
		t.Errorf("ID = %q, want test-engine", m.ID)
	}
	if m.Commit != "abc1234" {
		t.Errorf("Commit = %q, want abc1234", m.Commit)
	}
	// project_note и provenance.note — одно и то же по смыслу: что карта о себе
	// говорит. Страница не должна знать, в каком из двух полей это лежало.
	if !strings.Contains(m.Note, "убраны намеренно") {
		t.Errorf("Note = %q, want the project note", m.Note)
	}
	if len(m.Nodes) != 2 || m.Nodes[0].ID != "cmd-fin" {
		t.Fatalf("Nodes = %+v", m.Nodes)
	}
	if len(m.Flows) != 2 || len(m.Flows[0].Steps) != 2 {
		t.Fatalf("Flows = %+v", m.Flows)
	}
	if !m.Flows[1].Steps[0].Branch {
		t.Error("branch step lost its flag")
	}
	if m.Flows[0].Steps[1].Why != "живьём не прогонялось" {
		t.Errorf("why = %q", m.Flows[0].Steps[1].Why)
	}
}

// У карты движка нет раздела zones вовсе, а зона у каждого сценария есть. Без
// вывода зон из сценариев вкладка показала бы её пустой ровно там, где данные
// в файле лежат.
func TestLoad_zonesDerivedFromFlowsWhenAbsent(t *testing.T) {
	m := loadOne(t, engineMap)

	var names []string
	for _, z := range m.Zones {
		names = append(names, z.Name)
	}
	if got, want := strings.Join(names, "|"), "Деньги|Витрина"; got != want {
		t.Fatalf("zones = %q, want %q (порядок появления в сценариях)", got, want)
	}
	if m.Zones[0].Flows != 1 || m.Zones[0].Steps != 2 {
		t.Errorf("zone Деньги: flows=%d steps=%d, want 1 и 2", m.Zones[0].Flows, m.Zones[0].Steps)
	}
	// Приёмки у этой карты нет — и это НЕ «не принята»: третий ответ, «сверять
	// не с чем», должен отличаться от «принята» и от «отклонена».
	if m.Zones[0].Accepted {
		t.Error("зона названа принятой при отсутствующем разделе приёмки")
	}
}

func TestLoad_workspaceSchema(t *testing.T) {
	m := loadOne(t, coworkMap)

	if m.Commit != "" {
		t.Errorf("Commit = %q, want empty (карта не под git)", m.Commit)
	}
	if m.CheckedAt != "2026-08-08" {
		t.Errorf("CheckedAt = %q", m.CheckedAt)
	}
	if !strings.Contains(m.Note, "не под git") {
		t.Errorf("Note = %q, want provenance.note", m.Note)
	}
	// Корни — карта живёт в трёх деревьях, и якорь несёт имя дерева префиксом.
	// Без словаря корней «work:digest/run.sh:12» некуда развернуть.
	if len(m.Roots) != 2 {
		t.Fatalf("Roots = %+v, want 2", m.Roots)
	}
	if m.Roots[0].Name != "dotfiles" && m.Roots[0].Name != "work" {
		t.Errorf("Roots[0] = %+v", m.Roots[0])
	}
	if len(m.Findings) != 1 || m.Findings[0].Severity != "high" || m.Findings[0].Status != "починено" {
		t.Fatalf("Findings = %+v", m.Findings)
	}
	if m.Coverage == nil || m.Coverage.Scope.Root != "work" || len(m.Coverage.Exclusions) != 1 {
		t.Fatalf("Coverage = %+v", m.Coverage)
	}
	if m.Coverage.Exclusions[0].Why == "" {
		t.Error("исключение без причины — это дыра, а не исключение")
	}
	if m.Acceptance == nil || m.Acceptance.NotDone == "" || len(m.Acceptance.ClassesRun) != 1 {
		t.Fatalf("Acceptance = %+v", m.Acceptance)
	}
}

// Приёмка в файле лежит отдельным словарём, а читают её на зоне. Свести их
// должен адаптер: иначе страница держала бы своё правило соответствия, а
// правило, записанное дважды, однажды разъедется.
func TestLoad_acceptanceAttachedToZone(t *testing.T) {
	m := loadOne(t, coworkMap)

	if len(m.Zones) != 2 {
		t.Fatalf("Zones = %+v", m.Zones)
	}
	if !m.Zones[0].Accepted || !strings.Contains(m.Zones[0].Note, "найден 1 дефект") {
		t.Errorf("zone[0] = %+v, want accepted with its note", m.Zones[0])
	}
	// Явный список зон главнее сценариев: зона без единого сценария всё равно
	// существует, и молчать о ней значило бы скрыть непокрытое место.
	if m.Zones[1].Name != "Память" || m.Zones[1].Flows != 0 {
		t.Errorf("zone[1] = %+v, want Память с нулём сценариев", m.Zones[1])
	}
}

// Счёт непроверенных шагов делает адаптер, а не проза в карте: на карте движка
// раздел gaps говорил «пять шагов unverified» при четырёх — расхождение прозы с
// данными и есть причина считать это здесь.
func TestLoad_countsUnverifiedSteps(t *testing.T) {
	m := loadOne(t, engineMap)

	if m.Stats.Nodes != 2 || m.Stats.Flows != 2 || m.Stats.Steps != 3 {
		t.Errorf("Stats = %+v, want 2 узла, 2 сценария, 3 шага", m.Stats)
	}
	if m.Stats.Unverified != 1 {
		t.Errorf("Unverified = %d, want 1", m.Stats.Unverified)
	}
	if m.Stats.RuntimeChecks != 1 {
		t.Errorf("RuntimeChecks = %d, want 1", m.Stats.RuntimeChecks)
	}
}

// Пустой список Go пишет как null, и на этом дашборд однажды побелел целиком:
// у null нет .length. Разделы, которых в файле нет, обязаны приезжать списками.
func TestLoad_absentSectionsMarshalAsEmptyArrays(t *testing.T) {
	m := loadOne(t, engineMap)

	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"findings", "roots", "gaps", "runtime_checks", "nodes", "flows", "zones", "layers"} {
		got, ok := back[key]
		if !ok {
			t.Errorf("%s: отсутствует в JSON", key)
			continue
		}
		if strings.TrimSpace(string(got)) == "null" {
			t.Errorf("%s = null, want []", key)
		}
	}
}

func TestLoad_errors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"битый json", `{"project": "x"`, "map.json"},
		{"карта без имени проекта", `{"nodes": []}`, "project"},
		{"шаг ссылается на несуществующий узел", `{"project":"p","nodes":[{"id":"a"}],
			"flows":[{"id":"f","zone":"Z","steps":[{"n":1,"from":"a","to":"ghost","call":"c"}]}]}`, "ghost"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := archmap.Load(writeMap(t, "map.json", tt.body))
			if err == nil {
				t.Fatal("ошибки нет, а должна быть")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want mention of %q", err, tt.want)
			}
		})
	}
}

func TestLoad_missingFileNamesThePath(t *testing.T) {
	_, err := archmap.Load(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil || !strings.Contains(err.Error(), "nope.json") {
		t.Errorf("err = %v, want the path named", err)
	}
}

// Карты подключаются списком, и две карты одного проекта сделали бы вторую
// недостижимой по адресу: молчаливая потеря целого документа.
func TestLoadAll(t *testing.T) {
	a := writeMap(t, "map.json", engineMap)
	b := writeMap(t, "map.json", coworkMap)

	maps, err := archmap.LoadAll([]string{a, b})
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(maps) != 2 || maps[0].ID != "test-engine" || maps[1].ID != "test-workspace" {
		t.Fatalf("maps = %+v, want порядок как в аргументах", maps)
	}

	if _, err := archmap.LoadAll([]string{a, a}); err == nil ||
		!strings.Contains(err.Error(), "test-engine") {
		t.Errorf("дубль id: err = %v, want жалобу с именем карты", err)
	}
}
