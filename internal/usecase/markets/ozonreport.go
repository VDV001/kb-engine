package markets

import (
	"errors"

	"github.com/daniil/kb-engine/internal/domain"
)

// Ошибки отчета магазина.
var (
	ErrNotOzonReport = errors.New("это не отчет Ozon «По товарам»")
	ErrShopReport    = errors.New("это отчет магазина по товарам, а не выгрузка ниш")
)

// OzonReport — отчет Ozon «Аналитика → По товарам».
type OzonReport struct {
	Period     string
	PeriodDays int
	Total      *domain.ShopProduct
	Products   []*domain.ShopProduct
	Problems   []Problem
	Hints      map[string]string
}

// ReadOzonProducts reads the report.
func ReadOzonProducts(rows [][]string) (OzonReport, error) { return OzonReport{}, nil }
