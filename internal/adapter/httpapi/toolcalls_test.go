package httpapi_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/usecase/runs"
)

// callsStub — журнал вызовов под портом, который спрашивает витрина.
type callsStub struct {
	calls  []runs.Call
	exists bool
	err    error
}

func (c callsStub) Calls(int) (runs.CallLog, error) {
	return runs.CallLog{Exists: c.exists, Calls: c.calls}, c.err
}

var sample = []runs.Call{
	{Tool: "search_catalog", Query: "harness", At: time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC), OK: true},
	{Tool: "get_entry", Query: "#9999", At: time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC), OK: false},
}

// Вкладка «Ответы» показывает, о чём агент спрашивал базу. Без этого маршрута
// журнал копится, а увидеть его можно только командой в терминале — то есть
// половина смысла счётчика остаётся в консоли.
func TestToolCalls_servesWhatTheAgentAsked(t *testing.T) {
	h := newTestServerWith(httpapi.WithToolCalls(callsStub{calls: sample, exists: true}))
	rec := get(t, h, "/api/tool-calls")
	if rec.Code != 200 {
		t.Fatalf("код %d, ждали 200", rec.Code)
	}
	var out struct {
		Exists bool `json:"exists"`
		Total  int  `json:"total"`
		Calls  []struct {
			Tool  string `json:"tool"`
			Query string `json:"query"`
			At    string `json:"at"`
			OK    bool   `json:"ok"`
		} `json:"calls"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("ответ не разобрать: %v\n%s", err, rec.Body.String())
	}
	if !out.Exists || out.Total != 2 || len(out.Calls) != 2 {
		t.Fatalf("ответ = %+v", out)
	}
	if out.Calls[0].Tool != "search_catalog" || out.Calls[0].Query != "harness" {
		t.Errorf("первый вызов = %+v", out.Calls[0])
	}
	// Отказ обязан отличаться и на странице, а не только в журнале.
	if out.Calls[1].OK {
		t.Errorf("промах по id отдан как удачный вызов: %+v", out.Calls[1])
	}
}

// Журнала может не быть вовсе — движок поднимают и без него. Это законное
// состояние, и отвечать надо им, а не отказом: 404 на странице выглядит как
// поломка витрины, а пустой список — как «агент базу не спрашивал».
func TestToolCalls_withoutJournalSaysSoInsteadOfFailing(t *testing.T) {
	rec := get(t, newTestServer(), "/api/tool-calls")
	if rec.Code != 200 {
		t.Fatalf("код %d, ждали 200", rec.Code)
	}
	var out struct {
		Exists bool `json:"exists"`
		Total  int  `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Exists {
		t.Errorf("журнала нет, а ответ утверждает обратное: %+v", out)
	}
}

// ⚠️ Отрицательный контроль на утечку: наружу уходят ТОЛЬКО вызовы
// инструментов. Аргументы команд движка (`fin add --amount … --place …`) лежат
// в том же журнале и не имеют права попасть на страницу — там настоящие суммы
// и места владельца.
func TestToolCalls_neverCarriesEngineCommandArguments(t *testing.T) {
	// Порт отдаёт только вызовы по конструкции, поэтому проверяется то, что
	// проверяемо здесь: страница не выдумывает поля и отдаёт ровно их.
	h := newTestServerWith(httpapi.WithToolCalls(callsStub{calls: sample, exists: true}))
	body := get(t, h, "/api/tool-calls").Body.String()
	for _, secret := range []string{"418.50", "Сбербанк", "--amount", "--place"} {
		if strings.Contains(body, secret) {
			t.Fatalf("в ответе оказалось %q:\n%s", secret, body)
		}
	}
}

// ⚠️ Отсутствующий журнал приходит от ПОРТА, а не от отсутствия порта: сервер
// поднят с журналом, которого нет на диске. Прежний тест проверял другое —
// сервер вовсе без журнала, — и потому пропустил живой дефект: витрина
// отвечала «журнал заведён, вызовов нет» там, где файла не существовало.
func TestToolCalls_journalOnDiskMissingIsNotAnEmptyJournal(t *testing.T) {
	h := newTestServerWith(httpapi.WithToolCalls(callsStub{}))
	rec := get(t, h, "/api/tool-calls")
	var out struct {
		Exists bool `json:"exists"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Exists {
		t.Error("файла журнала нет, а ответ говорит, что он заведён")
	}
}
