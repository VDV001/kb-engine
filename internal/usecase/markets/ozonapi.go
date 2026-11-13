package markets

import "time"

// OzonItem — товар в отправлении Ozon.
type OzonItem struct {
	SKU, Article, Name string
	Qty                int
	Price              float64
}

// OzonPosting — отправление Ozon: схема FBO или FBS, статус и причина отмены.
type OzonPosting struct {
	Scheme, Status, CancelReason string
	Items                        []OzonItem
}

// OzonStock — остаток товара на складах, штук к продаже.
type OzonStock struct {
	SKU, Article string
	Qty          float64
}

// OzonShop is not written yet.
func OzonShop(posts []OzonPosting, stock []OzonStock, from, to time.Time) (OzonReport, error) {
	return OzonReport{}, nil
}
