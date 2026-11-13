package markets

import "time"

// OzonReturn — возврат или невыкуп Ozon: где товар сейчас и по какой цене.
type OzonReturn struct {
	Posting, SKU, Name string
	Type               string    // Cancellation — невыкуп, ClientReturn — возврат после покупки
	Status             string    // visual.status.sys_name: ReturnedToOzon, WriteOff, …
	Changed            time.Time // когда статус сменился
	Price              float64
}

// LossKind — почему отправление или возврат стоит проверить.
type LossKind string

// Виды возможных пропаж.
const (
	LossNoSale   LossKind = "no_sale"   // доставлено, а продажи в начислениях нет
	LossWriteOff LossKind = "write_off" // возврат списан Ozon
	LossUtilized LossKind = "utilized"  // возврат утилизирован
	LossLost     LossKind = "lost"      // Ozon пометил возврат «возможно потерян»
	LossStuck    LossKind = "stuck"     // возврат давно без движения
)

// ShopLoss — что проверить: отправление, товар, цена и с какого дня.
type ShopLoss struct {
	Kind         LossKind
	Posting, SKU string
	Name, Status string
	Price        float64
	Since        time.Time
}

// OzonLosses finds money that may be lost: доставленное без продажи и возвраты,
// которые до продавца не дошли.
func OzonLosses(posts []OzonPosting, acc []OzonAccrual, rets []OzonReturn, now time.Time) []ShopLoss {
	return nil
}
