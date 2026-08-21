package searchindex_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/searchindex"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), searchindex.FileName)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	p := write(t, `{"model":"тестовая","dims":2,"built":"2026-08-19",
		"vectors":{"10":[1,0],"20":[0,1]}}`)
	ix, err := searchindex.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ix.Model != "тестовая" || ix.Dims != 2 || len(ix.Vectors) != 2 {
		t.Fatalf("прочитано %+v", ix)
	}
	if len(ix.Vectors[10]) != 2 {
		t.Errorf("вектор записи 10 длины %d", len(ix.Vectors[10]))
	}
	// Поле built лежало в файле и не читалось никем — то есть «когда снят»
	// знал только тот, кто откроет json руками. Без него сообщение о пробелах
	// говорит, СКОЛЬКО записей потеряно, но не С КАКИХ пор (#254).
	if ix.Built != "2026-08-19" {
		t.Errorf("Built = %q, ожидалось 2026-08-19", ix.Built)
	}
}

// Отсутствие индекса — законное состояние, а не поломка: движок обязан искать
// остальными слоями. Но и молчать нельзя, поэтому отдельная ошибка.
func TestLoad_missing(t *testing.T) {
	_, err := searchindex.Load(filepath.Join(t.TempDir(), "нет.json"))
	if !errors.Is(err, searchindex.ErrNoIndex) {
		t.Fatalf("ошибка %v, ожидалась ErrNoIndex", err)
	}
}

func TestLoad_broken(t *testing.T) {
	if _, err := searchindex.Load(write(t, "не json")); err == nil {
		t.Fatal("битый файл принят молча")
	}
}

// Ключ, который не число, пропускается — одна испорченная строка не повод
// остаться без поиска целиком.
func TestLoad_skipsUnreadableKeys(t *testing.T) {
	ix, err := searchindex.Load(write(t, `{"model":"м","dims":2,"vectors":{"10":[1,0],"чепуха":[0,1]}}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ix.Vectors) != 1 {
		t.Errorf("векторов %d, ожидался один читаемый", len(ix.Vectors))
	}
}

func TestPathNextTo(t *testing.T) {
	got := searchindex.PathNextTo("/база/_data/catalog.json")
	want := filepath.Join("/база/_data", searchindex.FileName)
	if got != want {
		t.Errorf("PathNextTo = %q, ожидалось %q", got, want)
	}
}
