package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// fakeOzon answers like Seller API: один кабинет, Client-Id 42, ключ k-1.
func fakeOzon(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Client-Id") != "42" || r.Header.Get("Api-Key") != "k-1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		item := `{"sku":11,"offer_id":"100_46","name":"Брюки тестовые","quantity":1,"price":{"amount":"1000","currency":"RUB"}}`
		switch r.URL.Path {
		case "/v3/posting/fbo/list":
			posts := strings.Repeat(`{"status":"delivered","products":[`+item+`]},`, 9) +
				strings.Repeat(`{"status":"cancelled","cancellation":{"cancel_reason":"Покупатель отказался при вручении: товар не подошел"},"products":[`+item+`]},`, 7)
			_, _ = w.Write([]byte(`{"has_next":false,"cursor":"","postings":[` + strings.TrimSuffix(posts, ",") + `]}`))
		case "/v3/product/info/list":
			_, _ = w.Write([]byte(`{"items":[{"id":901,"offer_id":"100_46","name":"Брюки тестовые"},{"id":903,"offer_id":"300_M","name":"Пижама тестовая"}]}`))
		case "/v4/posting/fbs/list":
			_, _ = w.Write([]byte(`{"has_next":false,"cursor":"","postings":[]}`))
		case "/v1/finance/accrual/by-day":
			// Одна продажа 12.11 — последний день окна; остальные дни пустые.
			var body struct{ Date string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			acc := ""
			if body.Date == "2026-11-12" {
				acc = `{"accrual_id":1,"date":"2026-11-12","total_amount":{"amount":"462.50","currency":"RUB"},"accrued_category":"POSTING","posting":{"products":[{"sku":11,"quantity":1,` +
					`"delivery":{"services":[{"type_id":32,"accrued":{"amount":"-150","currency":"RUB"}},{"type_id":29,"accrued":{"amount":"-11.50","currency":"RUB"}}]},` +
					`"commission":{"sale_amount":{"amount":"1200","currency":"RUB"},"commission":{"amount":"-576","currency":"RUB"}}}]}}`
			}
			_, _ = w.Write([]byte(`{"accruals":[` + acc + `],"last_id":""}`))
		case "/v1/returns/list":
			// Один невыкуп Ozon списал месяц назад, второй вернулся на склад.
			_, _ = w.Write([]byte(`{"has_next":false,"returns":[` +
				`{"id":1,"type":"Cancellation","posting_number":"2-1","product":{"sku":33,"name":"Пижама тестовая","price":{"currency_code":"RUB","price":2000}},"visual":{"status":{"sys_name":"WriteOff"},"change_moment":"2026-10-10T10:00:00Z"}},` +
				`{"id":2,"type":"Cancellation","posting_number":"2-2","product":{"sku":11,"name":"Брюки тестовые","price":{"currency_code":"RUB","price":1000}},"visual":{"status":{"sys_name":"ReturnedToOzon"},"change_moment":"2026-10-11T10:00:00Z"}}]}`))
		case "/v4/product/info/stocks":
			_, _ = w.Write([]byte(`{"cursor":"","items":[{"product_id":901,"offer_id":"100_46","stocks":[{"type":"fbo","present":0,"reserved":0,"sku":11}]},{"product_id":903,"offer_id":"300_M","stocks":[{"type":"fbo","present":40,"reserved":0,"sku":33}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Кабинет Ozon — подключение окна: Client-Id в config.json, ключ в Связке;
// «Магазин» читает его и дает советы.
func TestMarketsOzon(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	if _, err := s.AddOzon("Тестовый магазин", "abc", "k-1", false); err == nil {
		t.Error("Client-Id of letters saved")
	}
	if _, err := s.AddOzon("Тестовый магазин", "42", " ", false); err == nil {
		t.Error("empty key saved")
	}
	row, err := s.AddOzon(" Тестовый магазин ", " 42 ", " k-1 ", false)
	if err != nil {
		t.Fatal(err)
	}
	if row.Kind != "ozon" || row.Name != "Тестовый магазин" || !row.OK || !strings.Contains(row.Detail, "42") {
		t.Errorf("row = %+v", row)
	}
	if tok.m[connKeyAccount(row.ID)] != "k-1" {
		t.Errorf("key in keychain = %q", tok.m[connKeyAccount(row.ID)])
	}
	if c, _ := s.snapshot().cfg.Connection(row.ID); c.Account != "42" || strings.Contains(c.Name+c.URL+c.Model, "k-1") {
		t.Errorf("config holds %+v; the key must not be there", c)
	}

	srv := fakeOzon(t)
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.Local)
	v, err := s.ozonShop(context.Background(), srv.URL, srv.Client(), row.ID, now, nil)
	if err != nil {
		t.Fatalf("ozonShop: %v", err)
	}
	if v.Source != "ozon" || v.File != "Ozon · Тестовый магазин" || v.PeriodDays != 90 || v.Period != "15.08.2026 – 12.11.2026" || len(v.Products) != 2 {
		t.Fatalf("view = %+v", v)
	}
	if p := v.Products[0]; p.SKU != "11" || p.Article != "100_46" || p.Ordered != 16 || p.Refused != 7 || p.Refuse != 0.4375 || !p.Urgent {
		t.Errorf("trousers = %+v", p)
	}
	kinds := []string{}
	for _, a := range v.Advice {
		kinds = append(kinds, a.Kind)
	}
	if strings.Join(kinds, " ") != "refusals supply dead" || v.Advice[0].SKUs[0] != "11" || v.Advice[2].Units != 40 {
		t.Errorf("advice = %+v", v.Advice)
	}

	// Ключ не уходит ни в данные экрана, ни в текст ошибки (#580: ключи — не в логи и не модели).
	if raw, _ := json.Marshal(v); strings.Contains(string(raw), "k-1") {
		t.Error("the view carries the key")
	}
	tok.m[connKeyAccount(row.ID)] = "wrong-secret"
	if _, err := s.ozonShop(context.Background(), srv.URL, srv.Client(), row.ID, now, nil); err == nil || !strings.Contains(err.Error(), "не принял") || strings.Contains(err.Error(), "wrong-secret") {
		t.Errorf("wrong key: err = %v, want words about the key and not the key itself", err)
	}
	if _, err := s.ozonShop(context.Background(), srv.URL, srv.Client(), "нет-такого", now, nil); err == nil {
		t.Error("unknown connection read")
	}
}

// Начисления: «Магазин» кабинета показывает, сколько пришло на счет, строками
// «Экономики магазина»; отказ Ozon в финансах не прячет товары.
func TestMarketsOzonEconomy(t *testing.T) {
	s, _, _ := newServerSettings(t)
	row, err := s.AddOzon("Тестовый магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := fakeOzon(t)
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.Local)
	v, err := s.ozonShop(context.Background(), srv.URL, srv.Client(), row.ID, now, nil)
	if err != nil {
		t.Fatalf("ozonShop: %v", err)
	}
	want := economyView{Sales: 1200, Commission: -576, Delivery: -150, Partners: -11.5, Total: 462.5}
	if v.Economy == nil || *v.Economy != want {
		t.Errorf("economy = %+v, want %+v", v.Economy, want)
	}
	// Пропажи: списанный невыкуп — проверить; вернувшийся на склад — нет.
	if l := v.Losses; l == nil || l.Err != "" || len(l.Rows) != 1 || l.Rows[0] != (lossRow{Kind: "write_off", Posting: "2-1", SKU: "33", Name: "Пижама тестовая", Price: 2000, Since: "10.10.2026"}) {
		t.Errorf("losses = %+v", v.Losses)
	}

	// Ключ без доступа к финансам: товары есть, у экономики — слова Ozon, без ключа.
	finance := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/finance/accrual/by-day" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		srv.Config.Handler.ServeHTTP(w, r)
	})
	refused := httptest.NewServer(finance)
	defer refused.Close()
	v, err = s.ozonShop(context.Background(), refused.URL, refused.Client(), row.ID, now, nil)
	if err != nil {
		t.Fatalf("ozonShop without finance: %v", err)
	}
	// Тот же ключ только что прочитал товары: отказ в финансах — нет роли, а не неверный ключ (ревью #594).
	if e := v.Economy; len(v.Products) != 2 || e == nil || !strings.Contains(e.Err, "Финансы") || strings.Contains(e.Err, "не принял") || strings.Contains(e.Err, "k-1") || e.Total != 0 {
		t.Errorf("refused finance: products %d, economy %+v", len(v.Products), v.Economy)
	}
	// Начисления есть, возвраты закрыты: причина — про возвраты, а не про «Финансы» начислений (ревью #595).
	noReturns := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/returns/list" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	defer noReturns.Close()
	if v, err := s.ozonShop(context.Background(), noReturns.URL, noReturns.Client(), row.ID, now, nil); err != nil || v.Losses == nil ||
		!strings.Contains(v.Losses.Err, "возврат") || strings.Contains(v.Losses.Err, "начислени") || v.Economy == nil || v.Economy.Total != 462.5 {
		t.Errorf("returns refused: err %v, losses %+v, economy %+v", err, v.Losses, v.Economy)
	}
	// Без начислений «доставлено без продажи» не проверить: окно говорит об этом, а не молчит.
	if l := v.Losses; l == nil || !strings.Contains(l.Err, "начислени") {
		t.Errorf("losses without accruals = %+v, want the reason", v.Losses)
	}
}

// «Загрузить из Ozon» называет пройденные этапы (отзыв владельца 13.11: по
// бегущей полосе не понять, где загрузка): отправления, остатки, начисления,
// возвраты. Этап с отказом не назван: окно не рисует ему ✓ (ревью #603).
func TestOzonShopStages(t *testing.T) {
	s, _, _ := newServerSettings(t)
	row, err := s.AddOzon("Тестовый магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := fakeOzon(t)
	noFin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/finance/accrual/by-day" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	defer noFin.Close()
	for _, tc := range []struct {
		name string
		srv  *httptest.Server
		want []string
	}{
		{"все прочитано", srv, []string{"accruals", "posts", "returns", "stock"}},
		{"начисления отказаны", noFin, []string{"posts", "returns", "stock"}},
	} {
		var mu sync.Mutex
		var got []string
		stage := func(stage string) { mu.Lock(); got = append(got, stage); mu.Unlock() }
		if _, err := s.ozonShop(context.Background(), tc.srv.URL, tc.srv.Client(), row.ID, time.Date(2026, 11, 13, 15, 0, 0, 0, time.Local), stage); err != nil {
			t.Fatal(err)
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: stages = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// «Магазин» за выбранные даты (отзыв владельца 13.11): режется из загруженных
// 90 дней без новых запросов; до загрузки, вне окна, задом наперед и у
// выключенного кабинета — отказ. Пропажи — всегда за все дни: они по возрасту
// (14 дней), и в коротком периоде их не было бы никогда (ревью #604).
func TestOzonShopPeriod(t *testing.T) {
	s, _, _ := newServerSettings(t)
	row, err := s.AddOzon("Тестовый магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ozonShopPeriod(row.ID, "2026-11-10", "2026-11-12"); err == nil {
		t.Error("period before any load accepted")
	}
	srv := fakeOzon(t)
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.Local)
	if _, err := s.ozonShop(context.Background(), srv.URL, srv.Client(), row.ID, now, nil); err != nil {
		t.Fatal(err)
	}
	srv.Close() // дальше — только загруженное

	v, err := s.ozonShopPeriod(row.ID, "2026-11-10", "2026-11-12")
	if err != nil {
		t.Fatalf("period: %v", err)
	}
	if v.Period != "10.11.2026 – 12.11.2026" || v.PeriodDays != 3 || v.Source != "ozon" || v.From != "2026-11-10" || v.WindowFrom != "2026-08-15" || v.WindowTo != "2026-11-12" {
		t.Errorf("view = %q %d %q from %q window %q–%q", v.Period, v.PeriodDays, v.Source, v.From, v.WindowFrom, v.WindowTo)
	}
	if v.Economy == nil || v.Economy.Total != 462.5 {
		t.Errorf("economy = %+v, want the 12.11 accrual only", v.Economy)
	}
	if v.Losses == nil || len(v.Losses.Rows) != 1 {
		t.Errorf("losses = %+v; the October write-off stays: losses are for all loaded days", v.Losses)
	}
	if v, err := s.ozonShopPeriod(row.ID, "2026-10-01", "2026-10-31"); err != nil || v.Economy.Total != 0 || len(v.Losses.Rows) != 1 {
		t.Errorf("October: err %v, economy %+v, losses %+v", err, v.Economy, v.Losses)
	}
	for _, bad := range [][2]string{{"2026-08-01", "2026-08-20"}, {"вчера", "2026-11-12"}} {
		if _, err := s.ozonShopPeriod(row.ID, bad[0], bad[1]); err == nil {
			t.Errorf("period %v accepted", bad)
		}
	}
	if _, err := s.ozonShopPeriod(row.ID, "2026-11-12", "2026-11-10"); !errors.Is(err, markets.ErrOzonPeriod) {
		t.Errorf("reversed: %v, want markets.ErrOzonPeriod", err)
	}
	if err := s.updateConfig(func(cfg *desktopconfig.Config) { cfg.Connections[0].Enabled = false }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ozonShopPeriod(row.ID, "2026-11-10", "2026-11-12"); err == nil {
		t.Error("disabled cabinet still served from memory")
	}
}

// «Магазин» открывается последней загрузкой (отзыв владельца 13.11): она лежит
// рядом с настройками и переживает перезапуск окна; «Загрузить из Ozon» —
// только по кнопке. Период режется и из сохраненной; удаление кабинета стирает ее.
func TestOzonShopLast(t *testing.T) {
	s, _, _ := newServerSettings(t)
	row, err := s.AddOzon("Тестовый магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ozonShopLast(row.ID); err == nil {
		t.Error("last load before any load")
	}
	srv := fakeOzon(t)
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.Local)
	if _, err := s.ozonShop(context.Background(), srv.URL, srv.Client(), row.ID, now, nil); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	s.shopLoads = shopLoadCache{} // как после перезапуска окна
	v, err := s.ozonShopLast(row.ID)
	if err != nil {
		t.Fatalf("last: %v", err)
	}
	if len(v.Products) != 2 || v.LoadedAt == "" || v.Economy == nil || v.Economy.Total != 462.5 {
		t.Errorf("last view: %d products, loaded %q, economy %+v", len(v.Products), v.LoadedAt, v.Economy)
	}
	s.shopLoads = shopLoadCache{}
	if p, err := s.ozonShopPeriod(row.ID, "2026-11-10", "2026-11-12"); err != nil || p.PeriodDays != 3 {
		t.Errorf("period from the saved load: %v, %+v", err, p.PeriodDays)
	}
	if err := s.dropShopLoad(row.ID); err != nil {
		t.Fatal(err)
	}
	s.shopLoads = shopLoadCache{}
	if _, err := s.ozonShopLast(row.ID); err == nil {
		t.Error("dropped load still read")
	}
}

// «План» берет остаток из кабинета: прогноз считается по продажам всего
// кабинета, поэтому и остаток — сумма по всем товарам (решение владельца 14.11).
func TestOzonPlanStock(t *testing.T) {
	s, _, _ := newServerSettings(t)
	row, err := s.AddOzon("Тестовый магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := fakeOzon(t)
	v, err := s.ozonPlanStock(context.Background(), srv.URL, srv.Client(), row.ID, nil)
	if err != nil {
		t.Fatalf("ozonPlanStock: %v", err)
	}
	if v.Qty != 40 || v.Products != 2 || v.InStock != 1 || v.Name != "Тестовый магазин" {
		t.Errorf("stock = %+v, want 40 шт: 2 товара, 1 с остатком", v)
	}
	// Список для выбора товаров — весь кабинет при любом выборе, с названиями из карточек.
	if len(v.Items) != 2 || v.Items[1] != (planStockItem{SKU: "33", Article: "300_M", Name: "Пижама тестовая", Qty: 40}) {
		t.Errorf("items = %+v", v.Items)
	}
	// Выбранные товары: остаток только их (решение владельца 14.11 — план по нише).
	for _, tc := range []struct {
		skus          []string
		qty           float64
		products, has int
	}{
		{[]string{"33"}, 40, 1, 1},
		{[]string{"11"}, 0, 1, 0},
		{[]string{"11", "33", "99"}, 40, 2, 1}, // SKU, которого в кабинете нет, не считается
	} {
		v, err := s.ozonPlanStock(context.Background(), srv.URL, srv.Client(), row.ID, tc.skus)
		if err != nil {
			t.Fatal(err)
		}
		if v.Qty != tc.qty || v.Products != tc.products || v.InStock != tc.has || len(v.Items) != 2 {
			t.Errorf("skus %v: stock = %+v, want %v шт, %d товаров, %d с остатком", tc.skus, v, tc.qty, tc.products, tc.has)
		}
	}
	if _, err := s.ozonPlanStock(context.Background(), srv.URL, srv.Client(), "нет-такого", nil); err == nil {
		t.Error("unknown connection read")
	}
}

// «Загрузить из Ozon» дописывает историю, а не затирает ее (#614): дни раньше
// 90-дневного окна остаются с прошлых загрузок, период выбирается по всем;
// разрыв между загрузками окно называет.
func TestOzonShopHistory(t *testing.T) {
	s, _, _ := newServerSettings(t)
	row, err := s.AddOzon("Тестовый магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := fakeOzon(t)
	now := time.Date(2026, 11, 13, 15, 0, 0, 0, time.Local)
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.Local) }
	old := shopLoad{Name: "Тестовый магазин", From: day(8, 1), To: day(8, 31), Now: day(9, 1), At: day(9, 1),
		Posts: []markets.OzonPosting{{Number: "0-1", Status: "delivered", At: day(8, 5).Add(10 * time.Hour), Items: []markets.OzonItem{{SKU: "77", Article: "77_old", Name: "Старый товар", Qty: 3, Price: 500}}}}}
	if err := s.saveShopLoad(row.ID, old); err != nil {
		t.Fatal(err)
	}
	v, err := s.ozonShop(context.Background(), srv.URL, srv.Client(), row.ID, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.WindowFrom != "2026-08-01" || v.WindowTo != "2026-11-12" || len(v.Gaps) != 0 {
		t.Errorf("window %s – %s, gaps %v; want 01.08 – 12.11 without gaps", v.WindowFrom, v.WindowTo, v.Gaps)
	}
	s.shopLoads = shopLoadCache{} // как после перезапуска окна: история — с диска
	p, err := s.ozonShopPeriod(row.ID, "2026-08-01", "2026-08-10")
	if err != nil {
		t.Fatalf("period before Ozon's 90 days: %v", err)
	}
	// Товары с остатком видны в любом периоде; заказы августа — только у старого.
	if i := slices.IndexFunc(p.Products, func(r shopRow) bool { return r.SKU == "77" }); i < 0 || p.Products[i].Ordered != 3 || p.PeriodDays != 10 {
		t.Errorf("August: %d days, products %+v; want the old one with 3 orders", p.PeriodDays, p.Products)
	}

	// Окно не загружалось дольше 90 дней — дни между загрузками названы пропуском.
	old.From, old.To = day(5, 1), day(5, 31)
	if err := s.saveShopLoad(row.ID, old); err != nil {
		t.Fatal(err)
	}
	s.shopLoads = shopLoadCache{}
	v, err = s.ozonShop(context.Background(), srv.URL, srv.Client(), row.ID, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.WindowFrom != "2026-05-01" || len(v.Gaps) != 1 || v.Gaps[0] != "01.06.2026 – 14.08.2026" {
		t.Errorf("window from %s, gaps %v; want 01.05 and the gap 01.06.2026 – 14.08.2026", v.WindowFrom, v.Gaps)
	}
}
