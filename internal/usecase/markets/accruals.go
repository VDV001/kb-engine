package markets

import (
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// AccrualKind — что за строка начисления Ozon.
type AccrualKind string

// Строки начисления.
const (
	AccrualSale       AccrualKind = "sale"       // продажа или возврат по цене продавца
	AccrualCommission AccrualKind = "commission" // вознаграждение Ozon
	AccrualService    AccrualKind = "service"    // услуга: статья — TypeID
	AccrualRest       AccrualKind = "rest"       // разница итога Ozon и суммы его частей
)

// OzonAccrual — строка начисления Ozon за день (#580, 3b).
type OzonAccrual struct {
	Day    time.Time // день начисления, полночь UTC
	SKU    string    // "" — начисление не по товару
	Kind   AccrualKind
	TypeID int // статья Ozon у услуги
	Amount domain.Money
}

// ShopEconomy — деньги магазина за период теми же строками, что «Экономика
// магазина» в кабинете Ozon; Total — сколько пришло на счет.
type ShopEconomy struct {
	Sales, Commission, Delivery, Promotion, Partners, Other, Total domain.Money
}

// OzonEconomy sums accruals into the cabinet's lines.
func OzonEconomy(acc []OzonAccrual) ShopEconomy {
	return ShopEconomy{}
}
