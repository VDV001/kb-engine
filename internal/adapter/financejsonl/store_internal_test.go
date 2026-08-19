package financejsonl

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// Строка журнала обязана пережить круг «запись → файл → чтение → запись»
// ЦЕЛИКОМ, и множество полей берётся из самой структуры line.
//
// Повод — замер (issue #229): внешний `TestSaveLoad_roundTripsEveryField`
// перечисляет поля руками и `repeat_of` не проверяет вовсе. Потеря этого поля
// сегодня краснеет в другом тесте (`TestRun_finAdd_forceLeavesATrace`), то есть
// класс прикрыт случайно — соседом, который писали про другое. Здесь он прикрыт
// по существу: новое поле строки валит тест до того, как окажется теряемым.
//
// Проверка внутренняя, потому что line не экспортирована, и стоит она на
// decode/encodeLine — обеих половинах круга сразу.
func TestLine_roundTripsEveryFieldOfTheWireShape(t *testing.T) {
	want := line{
		ID:          "01BX5ZZKBKACTAV9WEVGEMMVRZ",
		Kind:        "expense",
		Date:        "2026-04-06",
		Amount:      "418.00",
		Category:    "Транспорт",
		Subcategory: "Самокат",
		Place:       "Юрент",
		Description: "поездка до центра",
		Source:      "Чек",
		Account:     "Сбербанк",
		RepeatOf:    "01BX5ZZKBKACTAV9WEVGEMMVS0",
		Rev:         2,
		UpdatedAt:   "2026-04-07T10:00:00Z",
	}

	// Фикстура обязана быть непустой в КАЖДОМ поле: на нулевом значении
	// потерянное поле неотличимо от сохранённого, и тест стал бы декорацией.
	typ := reflect.TypeFor[line]()
	v := reflect.ValueOf(want)
	for field := range typ.Fields() {
		if v.FieldByName(field.Name).IsZero() {
			t.Fatalf("поле %s появилось в line, но фикстура его не заполняет — "+
				"допишите значение, иначе его потерю тест не заметит", field.Name)
		}
	}

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec, err := decode(raw, func() time.Time { return time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := encodeLine(rec); got != want {
		t.Errorf("строка не пережила круг:\n got %+v\nwant %+v", got, want)
	}
}
