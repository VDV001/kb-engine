package httpapi

import (
	"reflect"
	"strings"
)

// FinanceSummaryKeys — json-имена всех полей сводки финансов, взятые из самой
// структуры.
//
// Существует ради теста полноты: список разрезов, вписанный в тест руками,
// проверяет память автора, а не ответ сервера. Прежняя редакция перечисляла 12
// полей из 15, и `byAccount` с обоими полями про исключённые переводы могли
// пропасть из ответа при полностью зелёном наборе (issue #229).
//
// ⚠️ Лежит отдельным файлом, а НЕ в export_test.go: тот занят тестами выгрузки
// книги и к идиоме «экспорт для внешнего тест-пакета» отношения не имеет,
// несмотря на имя.
func FinanceSummaryKeys() []string {
	typ := reflect.TypeFor[financeSummaryDTO]()
	keys := make([]string, 0, typ.NumField())
	for field := range typ.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		keys = append(keys, name)
	}
	return keys
}
