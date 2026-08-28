package runs

import "time"

// Заготовка: символы объявлены, поведения нет — тесты рядом красные.

// Call — один вызов инструмента так, как его читает человек.
type Call struct {
	Tool  string
	Query string
	At    time.Time
	OK    bool
}

// Calls возвращает последние вызовы инструментов, новейшие сверху.
func Calls(j Journal, limit int) ([]Call, error) { return nil, nil }
