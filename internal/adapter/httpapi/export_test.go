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
// полей из 15, и `byAccount`, `excludedTransfers`, `excludedTransferCount`
// могли исчезнуть из ответа при полностью зелёном наборе (issue #229).
//
// Файл _test.go, поэтому в отгружаемый бинарь из него не попадает ничего.
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
