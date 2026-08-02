package catalogjson_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

// Живой каталог хранит код ответа с 01.08, но домен его не читал — и сводка,
// собранная поверх такого чтения, объявила бы живыми все проверенные записи,
// включая сотню отказов 403. Дефект того класса, который зелёный юнит-тест
// usecase не видит: там код подставляют руками.
func TestLoad_readsDriftHTTPCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	body := `{"entries":[
		{"id":1,"habr_id":1,"title":"отказ","url":"https://h/1","category":"golang","status":"keep",
		 "drift_check_date":"2026-08-01","drift_http_code":403},
		{"id":2,"habr_id":2,"title":"жива","url":"https://h/2","category":"golang","status":"keep",
		 "drift_check_date":"2026-08-01"}
	]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c, err := catalogjson.FileLoader{Path: path}.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	byID := map[int]*int{}
	for _, e := range c.Entries() {
		byID[e.ID()] = e.DriftHTTPCode()
	}
	if byID[1] == nil {
		t.Fatal("код ответа 403 потерян при чтении каталога")
	}
	if *byID[1] != 403 {
		t.Errorf("код = %d, want 403", *byID[1])
	}
	if byID[2] != nil {
		t.Errorf("у записи с ответом 200 код = %v, want nil (его не пишут)", *byID[2])
	}
}
