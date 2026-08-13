package catalogjson_test

import (
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

// Одна негодная запись гасила витрины целиком.
//
// Каталог читается на каждый запрос, а разбор был всё-или-ничего: первая
// запись, нарушившая инвариант, обрывала чтение, и /api/stats, /api/entries,
// /api/audits отвечали 500 разом. Полторы тысячи прочитанных записей
// выбрасывались из-за одной непрочитанной.
//
// Решение владельца 12.08: показывать остальное и называть виновную. Отсюда
// требование к числу на экране: частичные данные, поданные как полные, — тот
// самый обман, от которого вся эта база и защищается, поэтому пропущенное
// обязано быть посчитано и названо.
func TestDecode_skipsUnreadableEntriesAndNamesThem(t *testing.T) {
	const doc = `{"entries":[
		{"id":1,"title":"Живая","url":"https://h/1","category":"golang","status":"keep","lifecycle":"active"},
		{"id":2,"title":"Битая","url":"https://h/2","category":"golang","status":"НЕПОНЯТНО","lifecycle":"active"},
		{"id":3,"title":"Тоже живая","url":"https://h/3","category":"golang","status":"keep","lifecycle":"active"}
	]}`

	c, err := catalogjson.Decode(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("одна негодная запись обрушила чтение каталога: %v", err)
	}
	if got := len(c.Entries()); got != 2 {
		t.Errorf("прочитано %d записей, ожидалось 2", got)
	}

	bad := c.Unreadable()
	if len(bad) != 1 {
		t.Fatalf("непрочитанных названо %d, ожидалась одна: %+v", len(bad), bad)
	}
	if bad[0].ID != 2 {
		t.Errorf("названа запись #%d, а негодна была вторая", bad[0].ID)
	}
	// Причина нужна человеку, который пойдёт её чинить: «запись #2 не
	// прочитана» без причины отправляет искать наугад.
	if !strings.Contains(bad[0].Reason, "НЕПОНЯТНО") {
		t.Errorf("причина не называет, что именно негодно: %q", bad[0].Reason)
	}
}

// Порванный JSON — другое дело: там неизвестно даже, сколько записей в файле,
// и показывать «всё, кроме одной» не из чего. Отказ остаётся отказом.
func TestDecode_stillFailsOnBrokenJSON(t *testing.T) {
	if _, err := catalogjson.Decode(strings.NewReader(`{"entries":[{`)); err == nil {
		t.Error("порванный документ прочитался без ошибки")
	}
}

// Здоровый каталог не обязан ничего сообщать: «нечего сказать» и «есть
// находки» — разные ответы, и пустой список тут именно первое.
func TestDecode_healthyCatalogHasNothingUnreadable(t *testing.T) {
	const doc = `{"entries":[{"id":1,"title":"Живая","url":"https://h/1","category":"golang","status":"keep","lifecycle":"active"}]}`
	c, err := catalogjson.Decode(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := len(c.Unreadable()); got != 0 {
		t.Errorf("на здоровом каталоге названо %d непрочитанных", got)
	}
}
