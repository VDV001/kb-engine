package domain

import "errors"

// ErrInvalidShopProduct is returned when a shop product breaks its rules.
var ErrInvalidShopProduct = errors.New("товар магазина не подходит")

// ShopIssue — что не так с товаром магазина.
type ShopIssue string

// Проблемы товара.
const (
	IssueCancels ShopIssue = "cancels"
	IssueStock   ShopIssue = "stock"
	IssueReturns ShopIssue = "returns"
	IssueAds     ShopIssue = "ads"
	IssueRating  ShopIssue = "rating"
)

// ShopProductData — поля товара из отчета площадки.
type ShopProductData struct {
	Name, SKU, Article, Scheme, ABC, PriceIndex        string
	Ordered, Delivered, BoughtOut, Cancelled, Returned float64
	OrderedSum, AvgPrice, DRR, Rating                  float64
	Reviews, Stock, SupplyQty                          float64
	DaysOut, DaysOutOf, PromoDays, PeriodDays          int
	UrgentSupply                                       bool
}

// ShopProduct — товар своего магазина.
type ShopProduct struct{ d ShopProductData }

// NewShopProduct checks the product's rules.
func NewShopProduct(d ShopProductData) (*ShopProduct, error) { return &ShopProduct{d}, nil }

// Name is the product's name.
func (p *ShopProduct) Name() string { return p.d.Name }

// Data returns the fields.
func (p *ShopProduct) Data() ShopProductData { return p.d }

// BuyoutRate is bought out over ordered.
func (p *ShopProduct) BuyoutRate() float64 { return 0 }

// CancelRate is cancelled over ordered.
func (p *ShopProduct) CancelRate() float64 { return 0 }

// ReturnRate is returned over ordered.
func (p *ShopProduct) ReturnRate() float64 { return 0 }

// Issues lists what is wrong.
func (p *ShopProduct) Issues() []ShopIssue { return nil }
