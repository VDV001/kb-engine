package markets

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrUnknownShopTool — инструмента магазина с таким именем нет.
var ErrUnknownShopTool = errors.New("нет такого инструмента магазина")

// ShopData — кабинет для инструментов чата: отправления за период и остатки.
type ShopData struct {
	Name     string
	Posts    []OzonPosting
	Stock    []OzonStock
	From, To time.Time
	Loc      *time.Location // часовой пояс продавца: по нему дни и недели
}

// ShopToolSpec — инструмент для модели: имя, описание, JSON-схема аргументов.
type ShopToolSpec struct{ Name, Description, Params string }

// ShopTools are the shop's tools in the chat (#580, как Actions в примере
// api-master): модель сама зовет сводку, топ, «сколько везти» и карточку SKU.
// Только чтение; цифры — из кабинета, а не из памяти модели.
type ShopTools struct {
	// Load reads cabinet: имя кабинета словами человека, "" — первый подключенный.
	Load func(ctx context.Context, cabinet string) (ShopData, error)
}

// ShopToolsPrompt — правило для модели: откуда цифры и как о них говорить.
const ShopToolsPrompt = `
У человека подключен кабинет продавца Ozon. О его магазине (продажи, отмены, отказы, остатки, что довезти, почему упали продажи товара) спрашивай инструменты shop_*, а не угадывай. Сумма заказов включает отмены — это не выручка; выкуплено — по цене продажи, до комиссии и логистики Ozon. Называй период и товары с артикулом. Кабинетов несколько — передай cabinet (имя, как его назвал человек), иначе берется первый, и сводка называет его.`

// maxTop — сколько товаров отдает топ.
const maxTop = 20

// skuWeeks — сколько последних недель показывает карточка SKU.
const skuWeeks = 8

// Specs describes the tools.
func (ShopTools) Specs() []ShopToolSpec {
	return []ShopToolSpec{
		{"shop_summary", "Сводка магазина за период: заказано (с отменами), выкуплено на сумму, отмены, отказы при получении, остаток и советы — что довезти, что лежит, где отказываются.",
			`{"type":"object","properties":{"cabinet":{"type":"string","description":"имя кабинета, если их несколько"}}}`},
		{"shop_top", "Топ товаров по заказам, выкупу на сумму, отказам при получении или отменам. order: best — лучшие (больше заказов и выкупа, меньше отказов и отмен), worst — худшие (меньше заказов и выкупа — среди тех, что на складе; больше отказов и отмен); без order — самые заметные: больше всего заказов, выкупа, отказов или отмен.",
			`{"type":"object","properties":{"cabinet":{"type":"string","description":"имя кабинета, если их несколько"},"by":{"type":"string","enum":["ordered","bought","refused","cancelled"]},"order":{"type":"string","enum":["best","worst"]},"n":{"type":"integer","minimum":1,"maximum":20}},"required":["by"]}`},
		{"shop_supply", "Что довезти: товары, которых при нынешних продажах без отмен хватит меньше чем на две недели, и сколько штук на четыре.",
			`{"type":"object","properties":{"cabinet":{"type":"string","description":"имя кабинета, если их несколько"}}}`},
		{"shop_sku", "Карточка товара по названию, артикулу или SKU: заказы, выкуп, отмены, отказы, остаток и продажи по неделям с ценой — чтобы понять, почему продажи упали или выросли.",
			`{"type":"object","properties":{"cabinet":{"type":"string","description":"имя кабинета, если их несколько"},"q":{"type":"string","description":"название, артикул или SKU"}},"required":["q"]}`},
	}
}

type shopArgs struct {
	By, Order, Q, Cabinet string
	N                     int
}

// Run runs one tool; ошибка загрузки кабинета — ошибка, плохие аргументы — ответ модели.
func (t ShopTools) Run(ctx context.Context, name string, args []byte) (string, error) {
	if !slices.ContainsFunc(t.Specs(), func(s ShopToolSpec) bool { return s.Name == name }) {
		return "", fmt.Errorf("%w: %s", ErrUnknownShopTool, name)
	}
	var a shopArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "аргументы не разобраны: " + err.Error(), nil
		}
	}
	d, err := t.Load(ctx, a.Cabinet)
	if err != nil {
		return "", err
	}
	if d.Loc == nil {
		d.Loc = time.UTC
	}
	r, err := OzonShop(d.Posts, d.Stock, d.From, d.To)
	if err != nil {
		return "", err
	}
	switch name {
	case "shop_summary":
		return shopSummary(d, r), nil
	case "shop_top":
		return shopTop(r, a), nil
	case "shop_supply":
		return shopSupply(r), nil
	}
	return shopSKU(d, r, a.Q), nil
}

func shopSummary(d ShopData, r OzonReport) string {
	t := r.Total.Data()
	var b strings.Builder
	fmt.Fprintf(&b, "Кабинет «%s», %s (%d дн.), товаров %d\n", d.Name, r.Period, r.PeriodDays, len(r.Products))
	fmt.Fprintf(&b, "заказано %s шт на %s ₽ (с отменами)\n", num(t.Ordered), num(t.OrderedSum))
	fmt.Fprintf(&b, "выкуплено %s шт на %s ₽ (по цене продажи, до комиссии)\n", num(t.BoughtOut), num(t.BoughtOutSum))
	fmt.Fprintf(&b, "отменено %s шт (%s), из них отказ при получении %s шт; остаток %s шт\n", num(t.Cancelled), pct(t.Cancelled, t.Ordered), num(t.Refused), num(t.Stock))
	if n := len(r.Problems); n > 0 {
		fmt.Fprintf(&b, "не учтено товаров: %d (%s) — скажи человеку, что сводка без них\n", n, r.Problems[0].Reason)
	}
	names := map[domain.ShopAdviceKind]string{domain.AdviceRefusals: "отказываются при получении", domain.AdviceSupply: "довезти",
		domain.AdviceDeadStock: "лежит без заказов", domain.AdviceKeep: "продается чисто"}
	for _, a := range domain.AdviseShop(r.Products) {
		units := ""
		if a.Units > 0 { // у «продается чисто» штук нет
			units = ", " + num(a.Units) + " шт"
		}
		fmt.Fprintf(&b, "совет — %s: %d товаров%s\n", names[a.Kind], len(a.SKUs), units)
	}
	return b.String()
}

// topKeys — по чему строится топ.
var topKeys = map[string]func(domain.ShopProductData) float64{
	"ordered": func(d domain.ShopProductData) float64 { return d.Ordered }, "bought": func(d domain.ShopProductData) float64 { return d.BoughtOutSum },
	"refused": func(d domain.ShopProductData) float64 { return d.Refused }, "cancelled": func(d domain.ShopProductData) float64 { return d.Cancelled },
}

func shopTop(r OzonReport, a shopArgs) string {
	key := topKeys[a.By]
	if key == nil {
		return "by — одно из ordered, bought, refused, cancelled"
	}
	// Для отказов и отмен «худшие» — где их больше (ревью #593); без order —
	// самые заметные, по убыванию.
	bad := a.By == "refused" || a.By == "cancelled"
	desc := a.Order == "" || (a.Order == "worst") == bad
	ps := topCandidates(r, bad, desc)
	slices.SortStableFunc(ps, func(x, y domain.ShopProductData) int {
		if desc {
			return cmp.Compare(key(y), key(x))
		}
		return cmp.Compare(key(x), key(y))
	})
	n := a.N
	if n <= 0 || n > maxTop {
		n = 10
	}
	var b strings.Builder
	for i, d := range ps[:min(n, len(ps))] {
		fmt.Fprintf(&b, "%d. %s\n", i+1, productLine(d))
	}
	if b.Len() == 0 {
		return "товаров нет"
	}
	return b.String()
}

// topCandidates: снизу по заказам — только то, что на складе; снизу по
// отказам — только то, что заказывали.
func topCandidates(r OzonReport, bad, desc bool) []domain.ShopProductData {
	var ps []domain.ShopProductData
	for _, p := range r.Products {
		d := p.Data()
		if !desc && ((!bad && d.Stock <= 0) || (bad && d.Ordered < 1)) {
			continue
		}
		ps = append(ps, d)
	}
	return ps
}

func shopSupply(r OzonReport) string {
	var ps []domain.ShopProductData
	for _, p := range r.Products {
		if d := p.Data(); d.UrgentSupply {
			ps = append(ps, d)
		}
	}
	if len(ps) == 0 {
		return "довозить срочно нечего: у ходовых товаров запаса больше чем на две недели"
	}
	slices.SortStableFunc(ps, func(x, y domain.ShopProductData) int { return cmp.Compare(y.SupplyQty, x.SupplyQty) })
	var b strings.Builder
	for _, d := range ps {
		fmt.Fprintf(&b, "%s · %s · продано без отмен %s шт за %d дн. · остаток %s · довезти %s шт на 4 недели\n",
			d.Name, d.Article, num(d.Ordered-d.Cancelled), d.PeriodDays, num(d.Stock), num(d.SupplyQty))
	}
	return b.String()
}

// shopSKU — товар по названию, артикулу или SKU; размеры одной модели — все.
func shopSKU(d ShopData, r OzonReport, q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return "q пустой: назови товар, артикул или SKU"
	}
	fq := domain.FoldName(q)
	var hit []domain.ShopProductData
	for _, p := range r.Products {
		x := p.Data()
		if x.SKU == q || strings.Contains(domain.FoldName(x.Article), fq) || strings.Contains(domain.FoldName(x.Name), fq) {
			hit = append(hit, x)
		}
	}
	switch {
	case len(hit) == 0:
		return "товар «" + q + "» не найден в кабинете"
	case len(hit) > 6:
		var b strings.Builder
		fmt.Fprintf(&b, "подходят %d товаров, уточни у человека артикул или SKU; первые:\n", len(hit))
		for _, x := range hit[:6] {
			b.WriteString(productLine(x) + "\n")
		}
		return b.String()
	}
	var b strings.Builder
	for _, x := range hit {
		b.WriteString(productLine(x) + "\n")
		b.WriteString(weeks(d, x.SKU))
	}
	return b.String()
}

// weekStart — понедельник недели дня t в часовом поясе loc.
func weekStart(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
}

// weeks — заказы SKU по неделям с понедельника ВКЛЮЧАЯ отмены, из них
// отменено, средняя цена: у свежих недель отмены еще не закрылись, и ряд «без
// отмен» перекосил бы их против старых (как OzonDaily, ревью #593).
// Неполная неделя на краю окна подписана числом дней — это не падение.
func weeks(d ShopData, sku string) string {
	by := weekCounts(d, sku)
	var b strings.Builder
	b.WriteString("  по неделям (заказано с отменами · из них отменено · средняя цена):\n")
	first, last := weekStart(d.From, d.Loc), weekStart(d.To, d.Loc)
	if earliest := last.AddDate(0, 0, -7*(skuWeeks-1)); first.Before(earliest) {
		first = earliest
	}
	for w := first; !w.After(last); w = w.AddDate(0, 0, 7) {
		x := by[w]
		price := "—"
		if x.ordered > 0 {
			price = num(x.sum/x.ordered) + " ₽"
		}
		part := ""
		if n := partDays(w, d); n < 7 {
			part = fmt.Sprintf(" (неполная, %d дн.)", n)
		}
		fmt.Fprintf(&b, "  %s: %s шт · %s · %s%s\n", w.Format("02.01"), num(x.ordered), num(x.canc), price, part)
	}
	return b.String()
}

// weekCount — заказы недели: штук с отменами, из них отменено, сумма.
type weekCount struct{ ordered, canc, sum float64 }

// weekCounts sums the SKU's orders by week within the window.
func weekCounts(d ShopData, sku string) map[time.Time]weekCount {
	from, to := dayOf(d.From, d.Loc), dayOf(d.To, d.Loc)
	by := map[time.Time]weekCount{}
	for _, p := range d.Posts {
		if p.At.IsZero() {
			continue
		}
		if day := dayOf(p.At, d.Loc); day.Before(from) || day.After(to) {
			continue
		}
		for _, it := range p.Items {
			if it.SKU != sku {
				continue
			}
			k := weekStart(p.At, d.Loc)
			w, q := by[k], float64(it.Qty)
			w.ordered += q
			w.sum += q * it.Price
			if p.Status == "cancelled" {
				w.canc += q
			}
			by[k] = w
		}
	}
	return by
}

// dayOf — день t в часовом поясе loc, полночь.
func dayOf(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// partDays — сколько дней недели с понедельника w попало в окно.
func partDays(w time.Time, d ShopData) int {
	first, last := w, w.AddDate(0, 0, 6)
	if from := dayOf(d.From, d.Loc); from.After(first) {
		first = from
	}
	if to := dayOf(d.To, d.Loc); to.Before(last) {
		last = to
	}
	return calendarDays(first, last)
}

func productLine(d domain.ShopProductData) string {
	return fmt.Sprintf("%s · %s · SKU %s · заказано %s · выкуп %s · отказ %s · отмены %s · остаток %s · выкуплено на %s ₽",
		d.Name, d.Article, d.SKU, num(d.Ordered), pct(d.BoughtOut, d.Ordered), pct(d.Refused, d.Ordered), pct(d.Cancelled, d.Ordered), num(d.Stock), num(d.BoughtOutSum))
}

// num — целое с пробелом между тысячами: «102 000».
func num(v float64) string {
	s := strconv.FormatInt(int64(math.Round(v)), 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

func pct(part, whole float64) string {
	if whole <= 0 {
		return "—"
	}
	return strconv.Itoa(int(math.Round(part/whole*100))) + " %"
}
