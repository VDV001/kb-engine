package markets_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Фото против лидеров ниши (#608): лидеры — топ по выручке среди товаров с
// настоящими отзывами (в сыром топе MPStats мусор: продажи ровно 900 и 2500,
// цена 21 321 ₽ за футболку — замер 14.11).
func TestCheckPhotos(t *testing.T) {
	ps := []markets.NicheProduct{
		{SKU: 1, Name: "Мусор", Revenue: 9_000_000, Comments: 0, Rating: 0, Photos: 1},
		{SKU: 2, Name: "Лидер А", Revenue: 5_000_000, Comments: 300, Rating: 4.8, Photos: 8, Video: true},
		{SKU: 3, Name: "Лидер Б", Revenue: 4_000_000, Comments: 120, Rating: 4.7, Photos: 10, Video: true},
		{SKU: 4, Name: "Лидер В", Revenue: 3_000_000, Comments: 50, Rating: 4.6, Photos: 6},
		{SKU: 5, Name: "Мало отзывов", Revenue: 2_000_000, Comments: 3, Rating: 5, Photos: 12, Video: true},
	}
	got := markets.CheckPhotos(markets.PhotoCard{Photos: 4, Video: false, Rich: false}, ps, 10)
	if len(got.Leaders) != 3 || got.Leaders[0].SKU != 2 || got.Leaders[2].SKU != 4 {
		t.Fatalf("leaders %+v, want А, Б, В — без мусора и без товара с тремя отзывами", got.Leaders)
	}
	if got.MedianPhotos != 8 || got.VideoShare < 0.66 || got.VideoShare > 0.67 {
		t.Errorf("median %v video share %v, want 8 and 2/3", got.MedianPhotos, got.VideoShare)
	}
	kinds := map[string]bool{}
	for _, a := range got.Advice {
		kinds[a.Kind] = true
	}
	if !kinds["photos"] || !kinds["video"] || !kinds["rich"] {
		t.Errorf("advice %+v, want more photos, a video and rich content", got.Advice)
	}
	ok := markets.CheckPhotos(markets.PhotoCard{Photos: 9, Video: true, Rich: true}, ps, 10)
	if len(ok.Advice) != 0 {
		t.Errorf("advice %+v for a card on par with the leaders", ok.Advice)
	}
	none := markets.CheckPhotos(markets.PhotoCard{Photos: 4}, ps[:1], 10)
	if len(none.Leaders) != 0 || len(none.Advice) != 0 {
		t.Errorf("no real leaders — nothing to compare: %+v", none)
	}
}

// Живая проверка #608: топ ниши бывает одним продавцом в вариантах — лидер
// берется один на продавца, иначе сравнение — с одной карточкой.
func TestCheckPhotos_oneLeaderPerSeller(t *testing.T) {
	ps := []markets.NicheProduct{
		{SKU: 1, Seller: "Один", Revenue: 9, Comments: 50, Rating: 5, Photos: 6},
		{SKU: 2, Seller: "Один", Revenue: 8, Comments: 50, Rating: 5, Photos: 6},
		{SKU: 3, Seller: "Другой", Revenue: 7, Comments: 50, Rating: 5, Photos: 9},
	}
	got := markets.CheckPhotos(markets.PhotoCard{Photos: 6}, ps, 10)
	if len(got.Leaders) != 2 || got.Leaders[0].SKU != 1 || got.Leaders[1].SKU != 3 {
		t.Errorf("leaders %+v, want one per seller", got.Leaders)
	}
}
