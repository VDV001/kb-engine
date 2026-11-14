package markets

import (
	"cmp"
	"slices"
)

// NicheProduct — товар ниши из сервиса аналитики: выручка за период, отзывы,
// рейтинг, сколько фото и есть ли видео; Photo — главное фото, если прочитано.
type NicheProduct struct {
	SKU              int64
	Name, Seller     string
	Revenue, Price   float64
	Rating           float64
	Comments, Photos int
	Video            bool
	Photo            string
}

// PhotoCard — что есть у своей карточки: фото, видео, рич-контент.
type PhotoCard struct {
	Photos      int
	Video, Rich bool
}

// PhotoAdvice — совет по фото: kind — photos, video или rich.
type PhotoAdvice struct {
	Kind        string
	Have, Their float64 // у карточки и у лидеров (медиана фото или доля с видео)
}

// PhotoCheck — лидеры ниши, их медиана фото и доля с видео, советы.
type PhotoCheck struct {
	Leaders      []NicheProduct
	MedianPhotos float64
	VideoShare   float64
	Advice       []PhotoAdvice
}

// photoLeaderComments — меньше отзывов — не лидер: в сыром топе MPStats
// встречаются товары с ровными продажами без отзывов (замер 14.11).
const photoLeaderComments = 10

// photoLeadersOf — топ n по выручке среди товаров с отзывами, один на
// продавца: топ бывает одним продавцом в вариантах (живая проверка #608).
func photoLeadersOf(ps []NicheProduct, n int) []NicheProduct {
	cand := slices.Clone(ps)
	slices.SortStableFunc(cand, func(a, b NicheProduct) int { return cmp.Compare(b.Revenue, a.Revenue) })
	var out []NicheProduct
	sellers := map[string]bool{}
	for _, p := range cand {
		if len(out) == n || p.Comments < photoLeaderComments || p.Rating <= 0 || p.Seller != "" && sellers[p.Seller] {
			continue
		}
		sellers[p.Seller] = true
		out = append(out, p)
	}
	return out
}

// CheckPhotos compares the card's photos with the niche leaders (#608):
// топ n по выручке среди товаров с отзывами и рейтингом.
func CheckPhotos(card PhotoCard, ps []NicheProduct, n int) PhotoCheck {
	out := PhotoCheck{Leaders: photoLeadersOf(ps, n)}
	if len(out.Leaders) == 0 {
		return out
	}
	photos := make([]int, 0, len(out.Leaders))
	video := 0
	for _, p := range out.Leaders {
		photos = append(photos, p.Photos)
		if p.Video {
			video++
		}
	}
	slices.Sort(photos)
	m := len(photos) / 2
	out.MedianPhotos = float64(photos[m])
	if len(photos)%2 == 0 {
		out.MedianPhotos = float64(photos[m-1]+photos[m]) / 2
	}
	out.VideoShare = float64(video) / float64(len(out.Leaders))
	if float64(card.Photos) < out.MedianPhotos {
		out.Advice = append(out.Advice, PhotoAdvice{Kind: "photos", Have: float64(card.Photos), Their: out.MedianPhotos})
	}
	if !card.Video && out.VideoShare >= 0.5 {
		out.Advice = append(out.Advice, PhotoAdvice{Kind: "video", Their: out.VideoShare})
	}
	if !card.Rich { // рич-контент лидеров MPStats не отдает — совет без сравнения
		out.Advice = append(out.Advice, PhotoAdvice{Kind: "rich"})
	}
	return out
}
