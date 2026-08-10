package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testEngineMap = `{
  "project": "probe-engine",
  "commit": "abc1234",
  "nodes": [{"id": "cmd", "title": "команда", "layer": "commands", "kind": "service", "sources": ["cmd/kbengine/fin.go:1"]}],
  "flows": [{"id": "f1", "title": "Сценарий", "zone": "Деньги",
    "steps": [{"n": 1, "from": "cmd", "to": "cmd", "call": "Add", "source": "internal/usecase/finance/finance.go:1"}]}],
  "runtime_checks": ["прогнано живьём"]
}`

const testWorkspaceMap = `{
  "project": "probe-workspace",
  "checked_at": "2026-08-08",
  "zones": ["Автоматизация"],
  "nodes": [{"id": "job", "title": "задание", "layer": "jobs", "kind": "job", "sources": []}],
  "flows": [],
  "findings": [{"id": "f-1", "title": "находка", "severity": "high", "status": "починено"}]
}`

func writeMapFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "map.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write map: %v", err)
	}
	return path
}

// Карты подключаются флагом, и флаг можно повторить: карт две, живут они в
// разных деревьях, и сводить их в один путь пришлось бы придумывать разделитель
// поверх путей, где он уже может встретиться.
func TestBuildServeHandler_servesConnectedMaps(t *testing.T) {
	catalog := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"T","url":"https://h/","category":"golang","status":"keep"}
	]}`)
	h, err := buildServeHandler(catalog, "", "", "", "", "", "", "", "",
		[]string{writeMapFile(t, testEngineMap), writeMapFile(t, testWorkspaceMap)})
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/maps", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Maps []struct {
			ID    string `json:"id"`
			Stats struct {
				Nodes int `json:"nodes"`
				Steps int `json:"steps"`
			} `json:"stats"`
		} `json:"maps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Maps) != 2 || list.Maps[0].ID != "probe-engine" || list.Maps[1].ID != "probe-workspace" {
		t.Fatalf("maps = %+v, want порядок как во флагах", list.Maps)
	}
	if list.Maps[0].Stats.Steps != 1 {
		t.Errorf("шаги не посчитаны: %+v", list.Maps[0].Stats)
	}

	one := httptest.NewRecorder()
	h.ServeHTTP(one, httptest.NewRequest(http.MethodGet, "/api/maps/probe-workspace", nil))
	if one.Code != http.StatusOK || !strings.Contains(one.Body.String(), "находка") {
		t.Errorf("одна карта: status=%d body=%s", one.Code, one.Body.String())
	}
}

// Битую карту движок обязан заметить при запуске, а не при заходе на вкладку:
// в терминал смотрят в момент старта, а поймать ошибку посреди работы уже
// нельзя — смотрят тогда на страницу.
func TestBuildServeHandler_refusesToStartOnBrokenMap(t *testing.T) {
	catalog := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"T","url":"https://h/","category":"golang","status":"keep"}
	]}`)
	broken := writeMapFile(t, `{"project": "half"`)

	_, err := buildServeHandler(catalog, "", "", "", "", "", "", "", "", []string{broken})
	if err == nil {
		t.Fatal("движок поднялся с нечитаемой картой")
	}
	if !strings.Contains(err.Error(), "map.json") {
		t.Errorf("err = %v, want the file named", err)
	}
}

// Тот же ответ, что и у остальных восьми источников: флаг назван, и назван
// вслух — иначе пустая вкладка читается как поломка движка.
func TestRun_serve_announcesMapsSource(t *testing.T) {
	catalog := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"T","url":"https://h/","category":"golang","status":"keep"}
	]}`)

	var out, errb syncBuffer
	go func() {
		run([]string{"serve", "--catalog", catalog, "--addr", "127.0.0.1:0"}, &out, &errb)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "not connected") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := out.String(); !strings.Contains(got, "--maps") {
		t.Errorf("запуск без карт молчит о флаге:\n%s", got)
	}
}

func TestServeSources_includesMaps(t *testing.T) {
	srcs := serveSources("", "", "", "", "", "", "", "", []string{"/k/map.json"})

	var found bool
	for _, s := range srcs {
		if s.flag == "maps" {
			found = true
			if s.path == "" {
				t.Error("переданные карты числятся неподключённым источником")
			}
		}
	}
	if !found {
		t.Fatalf("источник maps не объявлен вовсе: %+v", srcs)
	}
	if strings.Contains(strings.Join(startupSources(serveSources("", "", "", "", "", "", "", "", nil)), "\n"),
		"connected: --maps") {
		t.Error("карты названы подключёнными без флага")
	}
}
