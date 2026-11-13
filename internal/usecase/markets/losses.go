package markets

import (
	"cmp"
	"slices"
	"time"
)

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

// lossAge — сколько ждать, прежде чем звать проверить: продажу Ozon начисляет
// в дни после доставки, возврат в пути едет неделю-другую.
// ponytail: две недели — на глаз по одному кабинету (13.11.2026); потолок —
// дальние регионы с долгой обратной логистикой; путь — порог в настройках модуля.
const lossAge = 14 * 24 * time.Hour

// returnLoss — вид пропажи по статусу возврата; stuck — если статус давний.
var returnLoss = map[string]LossKind{
	"WriteOff":             LossWriteOff,
	"Utilized":             LossUtilized,
	"PotentiallyLost":      LossLost,
	"WaitingShipment":      LossStuck,
	"MovingToOzon":         LossStuck,
	"MovingToSeller":       LossStuck,
	"ArrivedAtReturnPlace": LossStuck,
}

// OzonLosses finds money that may be lost, крупные сначала: доставлено давно,
// а продажи в начислениях нет; возврат списан, утилизирован, «возможно потерян»
// или давно стоит в пути. Компенсации Ozon платит общей суммой без номера
// отправления — оплачена ли пропажа, отсюда не видно.
func OzonLosses(posts []OzonPosting, acc []OzonAccrual, rets []OzonReturn, now time.Time) []ShopLoss {
	sold := map[string]bool{}
	for _, a := range acc {
		if a.Kind == AccrualSale && a.Unit != "" {
			sold[a.Unit] = true
		}
	}
	var out []ShopLoss
	for _, p := range posts {
		if p.Status != "delivered" || p.At.IsZero() || now.Sub(p.At) < lossAge || sold[p.Number] || len(p.Items) == 0 {
			continue
		}
		l := ShopLoss{Kind: LossNoSale, Posting: p.Number, SKU: p.Items[0].SKU, Name: p.Items[0].Name, Status: p.Status, Since: p.At}
		for _, it := range p.Items {
			l.Price += float64(it.Qty) * it.Price
		}
		out = append(out, l)
	}
	for _, r := range rets {
		k, ok := returnLoss[r.Status]
		if !ok || (k == LossStuck && (r.Changed.IsZero() || now.Sub(r.Changed) < lossAge)) {
			continue
		}
		out = append(out, ShopLoss{Kind: k, Posting: r.Posting, SKU: r.SKU, Name: r.Name, Status: r.Status, Price: r.Price, Since: r.Changed})
	}
	slices.SortStableFunc(out, func(a, b ShopLoss) int {
		return cmp.Or(cmp.Compare(b.Price, a.Price), cmp.Compare(a.Posting, b.Posting))
	})
	return out
}
