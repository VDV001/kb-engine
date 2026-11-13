package markets

import (
	"errors"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNoDaily is returned when a file has no sales by day.
var ErrNoDaily = errors.New("в файле нет продаж по дням")

// DailyFormat — как в файле лежат продажи по дням.
type DailyFormat string

// Виды файла.
const (
	DailyWide DailyFormat = "wide" // строки — товары, колонки — дни
	DailyLong DailyFormat = "long" // строка — день (и товар), колонка штук
)

// DailyInfo — что прочитано: вид, товаров, дней, период.
type DailyInfo struct {
	Format   DailyFormat
	Products int
	Days     int
	From, To time.Time
}

// ReadDaily reads sales by day, summed over the niche's products.
func ReadDaily(rows [][]string) (domain.DailySales, DailyInfo, error) {
	return domain.DailySales{}, DailyInfo{}, nil
}
