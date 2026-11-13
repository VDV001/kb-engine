package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidShopProduct is returned when a shop product breaks its rules.
var ErrInvalidShopProduct = errors.New("товар магазина не подходит")

// ShopIssue — что не так с товаром своего магазина (#580, «Мой магазин»).
type ShopIssue string

// Проблемы товара, в порядке важности.
const (
	IssueCancels ShopIssue = "cancels" // отменяют больше половины заказов
	IssueStock   ShopIssue = "stock"   // кончается или кончался остаток
	IssueReturns ShopIssue = "returns" // возвращают больше 10 %
	IssueAds     ShopIssue = "ads"     // реклама съедает больше 15 % выручки
	IssueRating  ShopIssue = "rating"  // рейтинг ниже 4,5
)

// Пороги проблем.
// ponytail: пороги из практики одежды на Ozon, не замерены по ее магазину.
// Потолок — другая категория; путь — пороги в настройках модуля.
const (
	issueMinOrders = 10   // меньше заказов — доли случайны
	issueCancels   = 0.5  // доля отмен
	issueReturns   = 0.1  // доля возвратов
	issueAds       = 0.15 // ДРР
	issueRating    = 4.5  // рейтинг при отзывах
	issueDaysOut   = 7    // дней без остатка
)

// ShopProductData — поля товара из отчета площадки; доли — 0..1.
type ShopProductData struct {
	Name, SKU, Article, Scheme, ABC, PriceIndex        string
	Ordered, Delivered, BoughtOut, Cancelled, Returned float64
	Refused                                            float64 // часть отмен: отказ при вручении (API Ozon)
	OrderedSum, AvgPrice, DRR, Rating                  float64
	BoughtOutSum                                       float64 // выкуплено на сумму по цене продажи; 0 — источник не знает
	Reviews, Stock, SupplyQty                          float64
	DaysOut, DaysOutOf, PromoDays, PeriodDays          int
	UrgentSupply                                       bool
}

// ShopProduct — товар своего магазина.
type ShopProduct struct{ d ShopProductData }

// NewShopProduct checks the product's rules.
func NewShopProduct(d ShopProductData) (*ShopProduct, error) {
	d.Name = strings.TrimSpace(d.Name)
	if why := d.broken(); why != "" {
		return nil, fmt.Errorf("%w: «%s» %s", ErrInvalidShopProduct, d.Name, why)
	}
	return &ShopProduct{d}, nil
}

// broken names the first broken rule; "" — правила соблюдены.
func (d ShopProductData) broken() string {
	switch {
	case d.Name == "":
		return "без названия"
	case min(d.Ordered, d.Delivered, d.BoughtOut, d.Cancelled, d.Returned, d.OrderedSum, d.BoughtOutSum, d.AvgPrice, d.Reviews, d.Stock, d.SupplyQty) < 0:
		return "число меньше нуля"
	case d.Ordered > 0 && (d.BoughtOut > d.Ordered || d.Cancelled > d.Ordered):
		return "выкуплено или отменено больше, чем заказано"
	case d.Refused < 0 || d.Refused > d.Cancelled:
		return "отказов при вручении больше, чем отмен"
	}
	return d.outOfRange()
}

// outOfRange names a share or a score outside its scale; "" — все в шкале.
func (d ShopProductData) outOfRange() string {
	switch {
	case d.Rating < 0 || d.Rating > 5:
		return "рейтинг вне 0-5"
	case d.DRR < 0 || d.DRR > 1:
		return "ДРР вне 0-100 %"
	case d.DaysOut < 0 || (d.DaysOutOf > 0 && d.DaysOut > d.DaysOutOf):
		return "дней без остатка больше, чем дней"
	}
	return ""
}

// Name is the product's name.
func (p *ShopProduct) Name() string { return p.d.Name }

// Data returns the fields.
func (p *ShopProduct) Data() ShopProductData { return p.d }

func (p *ShopProduct) rate(v float64) float64 {
	if p.d.Ordered <= 0 {
		return 0
	}
	return v / p.d.Ordered
}

// BuyoutRate is bought out over ordered.
func (p *ShopProduct) BuyoutRate() float64 { return p.rate(p.d.BoughtOut) }

// CancelRate is cancelled over ordered.
func (p *ShopProduct) CancelRate() float64 { return p.rate(p.d.Cancelled) }

// ReturnRate is returned over ordered.
func (p *ShopProduct) ReturnRate() float64 { return p.rate(p.d.Returned) }

// Issues lists what is wrong, by importance.
func (p *ShopProduct) Issues() []ShopIssue {
	var out []ShopIssue
	enough := p.d.Ordered >= issueMinOrders
	if enough && p.CancelRate() > issueCancels {
		out = append(out, IssueCancels)
	}
	if p.d.UrgentSupply || p.d.DaysOut >= issueDaysOut {
		out = append(out, IssueStock)
	}
	if enough && p.ReturnRate() > issueReturns {
		out = append(out, IssueReturns)
	}
	if p.d.DRR > issueAds {
		out = append(out, IssueAds)
	}
	if p.d.Reviews > 0 && p.d.Rating < issueRating {
		out = append(out, IssueRating)
	}
	return out
}

// RefusedRate is refused at pickup over ordered.
func (p *ShopProduct) RefusedRate() float64 { return p.rate(p.d.Refused) }
