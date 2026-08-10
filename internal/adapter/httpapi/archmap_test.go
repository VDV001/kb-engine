package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/archmap"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
)

func testMaps() []archmap.Map {
	return []archmap.Map{
		{
			ID: "test-engine", Project: "test-engine", Commit: "abc1234",
			Nodes: []archmap.Node{{ID: "cmd-fin", Title: "fin", Sources: []string{"cmd/kbengine/fin.go:123"}}},
			Flows: []archmap.Flow{{ID: "expense", Title: "Трата", Zone: "Деньги",
				Steps: []archmap.Step{{N: 1, From: "cmd-fin", To: "cmd-fin", Call: "Add"}}}},
			Zones: []archmap.Zone{{Name: "Деньги", Flows: 1, Steps: 1}},
			Stats: archmap.Stats{Nodes: 1, Flows: 1, Steps: 1},
		},
		{
			ID: "test-workspace", Project: "test-workspace", CheckedAt: "2026-08-08",
			Nodes:    []archmap.Node{{ID: "digest", Title: "Дайджест", Sources: []string{}}},
			Flows:    []archmap.Flow{},
			Zones:    []archmap.Zone{{Name: "Автоматизация", Accepted: true, Note: "принята"}},
			Findings: []archmap.Finding{{ID: "f-orphan", Title: "Сторож без вызывающего", Severity: "high"}},
			Stats:    archmap.Stats{Nodes: 1},
		},
	}
}

// Сервер собирается С фронтендом намеренно. Прежний тест неизвестного маршрута
// был зелёным, проверяя сборку без него, — то есть сервер вообще без фолбэка,
// который в отгружаемом бинаре как раз и стоит.
func serverWithMaps(load func() ([]archmap.Map, error)) http.Handler {
	front := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>SPA</html>")}}
	return httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil },
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{Maps: load}, testEngine, front)
}

// Список — это оглавление, и возить в нём сценарии со всеми шагами незачем:
// живые карты весят 136 и 120 КБ, а на вкладку заходят выбрать карту.
func TestMaps_listIsAnIndexNotTheWholeMap(t *testing.T) {
	rec := get(t, serverWithMaps(func() ([]archmap.Map, error) { return testMaps(), nil }), "/api/maps")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Maps []map[string]json.RawMessage `json:"maps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Maps) != 2 {
		t.Fatalf("maps = %d, want 2", len(body.Maps))
	}
	for _, heavy := range []string{"nodes", "flows", "findings"} {
		if _, present := body.Maps[0][heavy]; present {
			t.Errorf("оглавление несёт раздел %q целиком", heavy)
		}
	}
	for _, need := range []string{"id", "project", "stats"} {
		if _, present := body.Maps[0][need]; !present {
			t.Errorf("оглавление без поля %q", need)
		}
	}
}

func TestMaps_one(t *testing.T) {
	rec := get(t, serverWithMaps(func() ([]archmap.Map, error) { return testMaps(), nil }), "/api/maps/test-workspace")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var m archmap.Map
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.ID != "test-workspace" || len(m.Findings) != 1 || len(m.Nodes) != 1 {
		t.Fatalf("map = %+v", m)
	}
}

// Неизвестный адрес называет известные: карта подключается флагом, и «404» без
// списка не отвечает на единственный вопрос, который в этот момент возникает —
// какую из карт движку передали.
func TestMaps_unknownIDNamesTheKnownOnes(t *testing.T) {
	rec := get(t, serverWithMaps(func() ([]archmap.Map, error) { return testMaps(), nil }), "/api/maps/ghost")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "test-engine") {
		t.Errorf("body = %q, want the known addresses named", rec.Body.String())
	}
}

// Карты не переданы — это не поломка и не пустая база: это невыполненная
// просьба загрузить файл, и сказать об этом должен ответ, а не пустой экран.
func TestMaps_notConfigured(t *testing.T) {
	srv := serverWithMaps(nil)

	rec := get(t, srv, "/api/maps")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Пустой список именно списком: nil Go пишет как null, и .length у null
	// однажды обелил дашборд целиком.
	if got := strings.TrimSpace(rec.Body.String()); !strings.Contains(got, `"maps":[]`) {
		t.Errorf("body = %s, want maps:[]", got)
	}

	if rec := get(t, srv, "/api/maps/test-engine"); rec.Code != http.StatusNotFound {
		t.Errorf("одна карта без источника: status = %d, want 404", rec.Code)
	}
}

func TestMaps_loaderFailure(t *testing.T) {
	srv := serverWithMaps(func() ([]archmap.Map, error) { return nil, errors.New("map.json: unexpected end of JSON input") })

	rec := get(t, srv, "/api/maps")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unexpected end of JSON") {
		t.Errorf("body = %q, want the reason named", rec.Body.String())
	}
}

// Отрицательный контроль. Код ответа сам по себе не доказывает ничего: SPA
// отдаёт 200 на любой путь, и три выпуска подряд строка «N эндпоинтов 200»
// значила только это. Выдуманный сосед обязан давать 404 с телом, отличным от
// разметки страницы.
func TestMaps_neighbouringInventedPathIs404(t *testing.T) {
	srv := serverWithMaps(func() ([]archmap.Map, error) { return testMaps(), nil })

	for _, path := range []string{"/api/maps-nope", "/api/map/test-engine", "/api/mapz"} {
		rec := get(t, srv, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "SPA") {
			t.Errorf("%s: ответ — разметка страницы, а не отказ", path)
		}
	}
	// А настоящий маршрут при том же сервере отвечает телом, а не фолбэком.
	if rec := get(t, srv, "/api/maps"); !strings.Contains(rec.Body.String(), "test-engine") {
		t.Errorf("настоящий маршрут отдал %q", rec.Body.String())
	}
}
