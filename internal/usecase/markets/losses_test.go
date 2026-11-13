package markets_test

import (
	"slices"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

func TestOzonLosses(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	old, fresh := now.AddDate(0, 0, -30), now.AddDate(0, 0, -3)
	item := []markets.OzonItem{{SKU: "11", Name: "Брюки тестовые", Qty: 1, Price: 1500}}
	post := func(n, status string, at time.Time) markets.OzonPosting {
		return markets.OzonPosting{Number: n, Scheme: "FBO", Status: status, At: at, Items: item}
	}
	sale := func(unit string, kop int64) markets.OzonAccrual {
		return markets.OzonAccrual{Day: old, SKU: "11", Unit: unit, Kind: markets.AccrualSale, Amount: domain.NewMoney(kop)}
	}
	ret := func(n, status string, changed time.Time) markets.OzonReturn {
		return markets.OzonReturn{Posting: n, SKU: "22", Name: "Пижама тестовая", Type: "Cancellation", Status: status, Changed: changed, Price: 2000}
	}
	tests := []struct {
		name  string
		posts []markets.OzonPosting
		acc   []markets.OzonAccrual
		rets  []markets.OzonReturn
		want  []markets.LossKind
	}{
		{"доставлено давно, продажи нет — пропажа", []markets.OzonPosting{post("1-1", "delivered", old)},
			[]markets.OzonAccrual{{Day: old, Unit: "1-1", Kind: markets.AccrualService, TypeID: 32, Amount: domain.NewMoney(-12500)}}, nil, []markets.LossKind{markets.LossNoSale}},
		{"продажа начислена — не пропажа", []markets.OzonPosting{post("1-2", "delivered", old)}, []markets.OzonAccrual{sale("1-2", 150000)}, nil, nil},
		{"куплено и возвращено (продажа в ноль) — не пропажа", []markets.OzonPosting{post("1-3", "delivered", old)},
			[]markets.OzonAccrual{sale("1-3", 150000), sale("1-3", -150000)}, nil, nil},
		{"доставлено недавно — продажу еще не начислили", []markets.OzonPosting{post("1-4", "delivered", fresh)}, nil, nil, nil},
		{"отменено — продажи и не должно быть", []markets.OzonPosting{post("1-5", "cancelled", old)}, nil, nil, nil},
		{"возврат списан", nil, nil, []markets.OzonReturn{ret("2-1", "WriteOff", old)}, []markets.LossKind{markets.LossWriteOff}},
		{"возврат утилизирован", nil, nil, []markets.OzonReturn{ret("2-2", "Utilized", old)}, []markets.LossKind{markets.LossUtilized}},
		{"возможно потерян", nil, nil, []markets.OzonReturn{ret("2-3", "PotentiallyLost", fresh)}, []markets.LossKind{markets.LossLost}},
		{"застрял в пути больше двух недель", nil, nil, []markets.OzonReturn{ret("2-4", "WaitingShipment", old), ret("2-5", "MovingToOzon", old)}, []markets.LossKind{markets.LossStuck, markets.LossStuck}},
		{"в пути недавно — еще едет", nil, nil, []markets.OzonReturn{ret("2-6", "MovingToSeller", fresh)}, nil},
		{"создано давно, но последнее начисление свежее — еще едет (ревью #595)", []markets.OzonPosting{post("1-6", "delivered", old)},
			[]markets.OzonAccrual{{Day: fresh, Unit: "1-6", Kind: markets.AccrualService, TypeID: 32, Amount: domain.NewMoney(-12500)}}, nil, nil},
		{"только сторно продажи — продажи нет (ревью #595)", []markets.OzonPosting{post("1-7", "delivered", old)}, []markets.OzonAccrual{sale("1-7", -150000)}, nil, []markets.LossKind{markets.LossNoSale}},
		{"возврат FBS ждет в пункте — забрать, а не искать (ревью #595)", nil, nil,
			[]markets.OzonReturn{{Posting: "2-9", Scheme: "Fbs", Status: "ArrivedAtReturnPlace", Changed: old, Price: 2000}, {Posting: "2-10", Scheme: "Fbo", Status: "ArrivedAtReturnPlace", Changed: old, Price: 2000}},
			[]markets.LossKind{markets.LossStuck, markets.LossPickup}}, // равная цена — по номеру: "2-10" < "2-9"
		{"вернулся на склад или продавцу — не пропажа", nil, nil, []markets.OzonReturn{ret("2-7", "ReturnedToOzon", old), ret("2-8", "ReceivedBySeller", old)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := markets.OzonLosses(tt.posts, tt.acc, tt.rets, now)
			if len(got) != len(tt.want) {
				t.Fatalf("losses = %+v, want kinds %v", got, tt.want)
			}
			for i, l := range got {
				if l.Kind != tt.want[i] {
					t.Errorf("loss %d = %+v, want %s", i, l, tt.want[i])
				}
			}
		})
	}
}

func TestOzonLosses_fields(t *testing.T) {
	// Пропажа несет то, по чему ее искать в кабинете: отправление, товар, цену и день.
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	at := now.AddDate(0, 0, -40)
	got := markets.OzonLosses(
		[]markets.OzonPosting{{Number: "1-1", Status: "delivered", At: at, Items: []markets.OzonItem{{SKU: "11", Name: "Брюки тестовые", Qty: 2, Price: 1500}}}},
		nil,
		[]markets.OzonReturn{{Posting: "2-1", SKU: "22", Name: "Пижама тестовая", Status: "WriteOff", Changed: at, Price: 2000}}, now)
	// Крупные сначала: 2 × 1 500 по отправлению, потом возврат на 2 000.
	want := []markets.ShopLoss{
		{Kind: markets.LossNoSale, Posting: "1-1", SKU: "11", Name: "Брюки тестовые", Status: "delivered", Price: 3000, Since: at},
		{Kind: markets.LossWriteOff, Posting: "2-1", SKU: "22", Name: "Пижама тестовая", Status: "WriteOff", Price: 2000, Since: at},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("losses = %+v\nwant %+v", got, want)
	}
}

func TestOzonLosses_rowsByItemAndQuantity(t *testing.T) {
	// Несколько товаров в отправлении — строка на товар со своей ценой; у возврата
	// цена умножена на штуки; без даты статуса пропажа остается, дня у нее нет.
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	at := now.AddDate(0, 0, -40)
	got := markets.OzonLosses(
		[]markets.OzonPosting{{Number: "1-1", Status: "delivered", At: at, Items: []markets.OzonItem{{SKU: "11", Name: "Брюки тестовые", Qty: 1, Price: 1500}, {SKU: "33", Name: "Пижама тестовая", Qty: 1, Price: 4000}}}},
		nil, []markets.OzonReturn{{Posting: "2-1", SKU: "22", Name: "Футболка тестовая", Status: "WriteOff", Qty: 3, Price: 2000}}, now)
	want := []markets.ShopLoss{
		{Kind: markets.LossWriteOff, Posting: "2-1", SKU: "22", Name: "Футболка тестовая", Status: "WriteOff", Price: 6000},
		{Kind: markets.LossNoSale, Posting: "1-1", SKU: "33", Name: "Пижама тестовая", Status: "delivered", Price: 4000, Since: at},
		{Kind: markets.LossNoSale, Posting: "1-1", SKU: "11", Name: "Брюки тестовые", Status: "delivered", Price: 1500, Since: at},
	}
	if !slices.Equal(got, want) {
		t.Errorf("losses = %+v\nwant %+v", got, want)
	}
}
