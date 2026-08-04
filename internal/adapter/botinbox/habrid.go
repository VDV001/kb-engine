package botinbox

import (
	"regexp"
	"strconv"
)

// habrArticleURL — адрес статьи на Хабре во всех формах, которые встречаются в
// живом каталоге: 888 записей вида /ru/articles/N, 378 корпоративных вида
// /ru/companies/<блог>/articles/N, плюс исторические /post/N и /blog/N.
//
// Хост проверяется явно. Без этого «/articles/123» с любого другого сайта
// получил бы номер статьи Хабра — а выдуманный идентификатор хуже пустого поля:
// по нему потом ищут дубли.
var habrArticleURL = regexp.MustCompile(`^https?://(?:[a-z0-9-]+\.)*habr\.com/\S*?(?:articles|post|blog)/(\d+)`)

// HabrIDFromURL достаёт номер статьи из адреса, или 0, когда адрес его не несёт.
//
// Поле habr_id заполнялось у 36 записей из 717, пришедших из инбокса, — то есть
// не заполнялось никогда, а эти 36 проставлены руками. Цена измерена: проверка
// «нет ли такой статьи в базе» по этому полю смотрела на 5% инбокса и молчала,
// а молчание читается как «дубликатов нет».
func HabrIDFromURL(url string) int {
	m := habrArticleURL.FindStringSubmatch(url)
	if m == nil {
		return 0
	}
	id, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return id
}
