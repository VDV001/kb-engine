package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// Витрина о валюте не знала ничего: #332 заведена вопросом владельца к экрану —
// «почему тут рублёвый счёт, я же получил наличными». Домен научился валюте
// раньше (PR #336), запись пришла следующей (#340), а API продолжал отдавать
// одно число без единицы, и страница показывала цену входа как текущую оценку.
//
// Имена счетов здесь ВЫДУМАННЫЕ: гейт данных владельца читает маркеры из живой
// книги, и настоящее имя в фикстуре — утечка, а не удобство.

type currencyFinance struct{}

func (currencyFinance) Summary([]string) (finance.Summary, error) {
	return finance.Summary{}, nil
}

func (currencyFinance) Finances() (httpapi.Finances, error) {
	now := func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }
	updated := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)

	rub, _ := domain.ParseMoney("1000.00")
	plain, _ := domain.NewAccount("Первый банк", rub, updated, now)

	usd, _ := domain.NewCurrency("USD")
	amount, _ := domain.ParseMoney("500.00")
	perUnit, _ := domain.ParseMoney("84.28")
	rate, _ := domain.NewRate(perUnit)
	valued, _ := domain.NewForeignAccount("Кубышка → Доллары", amount, usd, rate, updated, now)

	eur, _ := domain.NewCurrency("EUR")
	other, _ := domain.ParseMoney("200.00")
	noRate, _ := domain.NewForeignAccount("Кубышка → Евро", other, eur, domain.Rate{}, updated, now)

	return httpapi.Finances{
		Accounts: []domain.Account{plain, valued, noRate},
	}, nil
}

func currencyServer() http.Handler {
	return httpapi.NewServer(fakeQuery{}, fakeAudit{}, fakeAnalytics{}, currencyFinance{},
		func() (analyticsconfig.Config, error) { return testConfig, nil }, nil,
		httpapi.Documents{}, testEngine, nil)
}

type accountJSON struct {
	Bank      string `json:"bank"`
	Balance   string `json:"balance"`
	Currency  string `json:"currency"`
	Rate      string `json:"rate"`
	BaseValue string `json:"base_value"`
	Unvalued  bool   `json:"unvalued"`
}

type rateJSON struct {
	Currency string `json:"currency"`
	PerUnit  string `json:"per_unit"`
	On       string `json:"on"`
}

type groupJSON struct {
	Group    string     `json:"group"`
	Total    string     `json:"total"`
	Unvalued []string   `json:"unvalued"`
	Rates    []rateJSON `json:"rates"`
}

type financeAccountsBody struct {
	Accounts []accountJSON `json:"accounts"`
	Groups   []groupJSON   `json:"groups"`
}

func financeAccounts(t *testing.T) financeAccountsBody {
	t.Helper()
	rec := get(t, currencyServer(), "/api/finances")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body financeAccountsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// Сумма в своей валюте и единица рядом с ней. Без кода валюты «500.00» у
// долларового счёта читается как рубли — ровно тот дефект, из-за которого
// issue и заведена.
func TestFinances_accountCarriesItsCurrency(t *testing.T) {
	body := financeAccounts(t)
	if len(body.Accounts) != 3 {
		t.Fatalf("счетов %d, ждали 3", len(body.Accounts))
	}
	usd := body.Accounts[1]
	if usd.Currency != "USD" {
		t.Errorf("currency = %q, ждали USD", usd.Currency)
	}
	if usd.Balance != "500.00" {
		t.Errorf("balance = %q, ждали сумму в своей валюте 500.00", usd.Balance)
	}
	if usd.Rate != "84.28" {
		t.Errorf("rate = %q, ждали 84.28", usd.Rate)
	}
	if usd.BaseValue != "42140.00" {
		t.Errorf("base_value = %q, ждали 42140.00", usd.BaseValue)
	}
}

// Рублёвый счёт остаётся ровно таким, каким был: пустая валюта и никакого
// курса. Иначе каждая существующая страница обязана была бы научиться новому,
// чтобы показать прежнее.
func TestFinances_rubleAccountStaysQuiet(t *testing.T) {
	body := financeAccounts(t)
	plain := body.Accounts[0]
	if plain.Currency != "" || plain.Rate != "" {
		t.Errorf("рублёвый счёт = %+v, ждали пустые валюту и курс", plain)
	}
	if plain.Unvalued {
		t.Error("рублёвый счёт помечен неоценённым")
	}
}

// «Оценить нечем» — отдельный ответ, а не ноль: ноль читается как «денег нет»,
// и это ложь противоположного знака.
func TestFinances_unknownRateIsNotZero(t *testing.T) {
	body := financeAccounts(t)
	eur := body.Accounts[2]
	if !eur.Unvalued {
		t.Error("счёт без курса не помечен неоценённым")
	}
	if eur.Currency != "EUR" {
		t.Errorf("currency = %q, ждали EUR", eur.Currency)
	}
	if eur.Rate != "" {
		t.Errorf("rate = %q, ждали пусто: курса нет", eur.Rate)
	}
}

// Итог по роду считает usecase и присылает готовым. До этой правки веб-витрина
// складывала группы сама, в TypeScript, — то есть правило про деньги жило в
// двух экземплярах, и вторая копия о валюте не знала вовсе: она сложила бы
// доллары с рублями и назвала результат рублями.
func TestFinances_groupTotalsComeFromUsecase(t *testing.T) {
	body := financeAccounts(t)
	var kubyshka *groupJSON
	for i := range body.Groups {
		if body.Groups[i].Group == "Кубышка" {
			kubyshka = &body.Groups[i]
		}
	}
	if kubyshka == nil {
		t.Fatalf("рода «Кубышка» нет в ответе: %+v", body.Groups)
	}
	if kubyshka.Total != "42140.00" {
		t.Errorf("итог рода = %q, ждали 42140.00 (евро не оценены и не входят)", kubyshka.Total)
	}
	if len(kubyshka.Unvalued) != 1 || kubyshka.Unvalued[0] != "Евро" {
		t.Errorf("неоценённые = %v, ждали [Евро]", kubyshka.Unvalued)
	}
	if len(kubyshka.Rates) != 1 {
		t.Fatalf("курсов %d, ждали 1", len(kubyshka.Rates))
	}
	if r := kubyshka.Rates[0]; r.Currency != "USD" || r.PerUnit != "84.28" || r.On != "2026-08-30" {
		t.Errorf("курс = %+v, ждали USD 84.28 на 2026-08-30", r)
	}
}
