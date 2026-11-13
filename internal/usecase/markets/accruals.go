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
	Unit   string    // номер отправления у начисления по нему; "" — не по отправлению
	Kind   AccrualKind
	TypeID int // статья Ozon у услуги
	Qty    int // штук товара в начислении; 0 — не по товару
	Amount domain.Money
}

// ShopEconomy — деньги магазина за период теми же строками, что «Экономика
// магазина» в кабинете Ozon; Total — сколько пришло на счет.
type ShopEconomy struct {
	Sales, Commission, Delivery, Promotion, Partners, Other, Total domain.Money
}

// economyGroup — строка «Экономики магазина», в которую идет статья Ozon.
type economyGroup int

const (
	groupOther economyGroup = iota
	groupDelivery
	groupPromotion
	groupPartners
)

// ozonGroups раскладывает статьи (/v1/finance/accrual/types) по строкам кабинета.
// Сверено до рубля с «Экономикой магазина» настоящего кабинета за два месяца
// (13.11.2026); статья не из списка — в «Остальном», на счет она идет все равно.
// ponytail: «Звездные товары» (74) в услугах партнеров подтверждены одним месяцем;
// потолок — новая статья Ozon уходит в «Остальное»; путь — сверка по новым экранам.
var ozonGroups = map[int]economyGroup{
	32: groupDelivery,  // логистика
	59: groupDelivery,  // обратная логистика
	98: groupDelivery,  // доставка до места выдачи силами Ozon
	41: groupPromotion, // оплата за клик
	54: groupPromotion, // продвижение товара
	29: groupPartners,  // доставка до места выдачи
	1:  groupPartners,  // эквайринг
	45: groupPartners,  // обработка возвратов, отмен и невыкупов партнерами
	39: groupPartners,  // упаковка товара партнерами
	79: groupPartners,  // временное размещение товара партнерами
	74: groupPartners,  // звездные товары
}

// OzonEconomy sums accruals into the cabinet's lines; Total — все строки.
func OzonEconomy(acc []OzonAccrual) ShopEconomy {
	var e ShopEconomy
	for _, a := range acc {
		e.add(a)
	}
	return e
}

// add puts one accrual line into its row and into Total.
func (e *ShopEconomy) add(a OzonAccrual) {
	line := &e.Other
	switch a.Kind {
	case AccrualSale:
		line = &e.Sales
	case AccrualCommission:
		line = &e.Commission
	case AccrualService:
		switch ozonGroups[a.TypeID] {
		case groupDelivery:
			line = &e.Delivery
		case groupPromotion:
			line = &e.Promotion
		case groupPartners:
			line = &e.Partners
		}
	}
	*line = line.Add(a.Amount)
	e.Total = e.Total.Add(a.Amount)
}
