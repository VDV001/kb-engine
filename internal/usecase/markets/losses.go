package markets

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// OzonReturn — возврат или невыкуп Ozon: где товар сейчас и по какой цене.
type OzonReturn struct {
	Posting, SKU, Name string
	Type               string    // Cancellation — невыкуп, ClientReturn — возврат после покупки
	Scheme             string    // Fbo или Fbs: возврат FBS продавец забирает сам
	Status             string    // visual.status.sys_name: ReturnedToOzon, WriteOff, …
	Changed            time.Time // когда статус сменился
	Qty                int       // штук; 0 — Ozon не сказал, считается за одну
	Price              float64   // за штуку
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
	LossPickup   LossKind = "pickup"    // возврат FBS давно ждет продавца в пункте
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
	out := append(unsold(posts, acc, now), returnLosses(rets, now)...)
	slices.SortStableFunc(out, func(a, b ShopLoss) int {
		return cmp.Or(cmp.Compare(b.Price, a.Price), cmp.Compare(a.Posting, b.Posting))
	})
	return out
}

// unsold lists postings delivered with no positive sale in the accruals: строка
// на товар. Даты доставки в API нет — возраст считается от последнего начисления
// по отправлению (логистику Ozon начисляет при отгрузке и доставке), а без них —
// от создания (ревью #595).
func unsold(posts []OzonPosting, acc []OzonAccrual, now time.Time) []ShopLoss {
	sold, last := map[string]bool{}, map[string]time.Time{}
	for _, a := range acc {
		if a.Unit == "" {
			continue
		}
		if a.Kind == AccrualSale && a.Amount.Kopecks() > 0 {
			sold[a.Unit] = true // одно сторно без продажи — не продажа
		}
		if a.Day.After(last[a.Unit]) {
			last[a.Unit] = a.Day
		}
	}
	var out []ShopLoss
	for _, p := range posts {
		since := p.At
		if last[p.Number].After(since) {
			since = last[p.Number]
		}
		if p.Status != "delivered" || p.At.IsZero() || now.Sub(since) < lossAge || sold[p.Number] {
			continue
		}
		for _, it := range p.Items {
			out = append(out, ShopLoss{Kind: LossNoSale, Posting: p.Number, SKU: it.SKU, Name: it.Name, Status: p.Status, Price: float64(it.Qty) * it.Price, Since: since})
		}
	}
	return out
}

// returnLosses lists returns that did not come back: списан, утилизирован,
// «возможно потерян» или lossAge без движения.
func returnLosses(rets []OzonReturn, now time.Time) []ShopLoss {
	var out []ShopLoss
	for _, r := range rets {
		k, ok := returnLoss[r.Status]
		if !ok || (k == LossStuck && (r.Changed.IsZero() || now.Sub(r.Changed) < lossAge)) {
			continue
		}
		if r.Status == "ArrivedAtReturnPlace" && strings.EqualFold(r.Scheme, "fbs") {
			k = LossPickup // FBS забирает продавец: ждет его, а не потерян (ревью #595)
		}
		out = append(out, ShopLoss{Kind: k, Posting: r.Posting, SKU: r.SKU, Name: r.Name, Status: r.Status, Price: float64(max(r.Qty, 1)) * r.Price, Since: r.Changed})
	}
	return out
}
