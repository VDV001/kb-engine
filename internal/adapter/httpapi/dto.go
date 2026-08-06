package httpapi

import (
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
	"github.com/daniil/kb-engine/internal/usecase/freshness"
)

// transactionDTO is one ledger row on the wire.
//
// Amount is a decimal string, not a number: the ledger keeps kopecks as int64
// precisely so that 89.99 does not become 89.98999999999999, and putting it
// through a JSON float would undo that on the way to the screen.
type transactionDTO struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Date        string `json:"date"`
	Amount      string `json:"amount"`
	Category    string `json:"category,omitempty"`
	Subcategory string `json:"subcategory,omitempty"`
	Place       string `json:"place,omitempty"`
	Description string `json:"description,omitempty"`
	Account     string `json:"account,omitempty"`
	Source      string `json:"source,omitempty"`
	// RecordedAt — момент появления строки в книге, RFC3339. Пусто означает
	// «неизвестен», а не «давно»: строку могли вписать мимо движка.
	RecordedAt string `json:"recorded_at,omitempty"`
}

// namedTotalDTO is one row of a breakdown: a name, what it adds up to, and how
// many rows went into it. Used for every cut whose key is a single string —
// category, place, payment source, income source.
type namedTotalDTO struct {
	Name  string `json:"name"`
	Total string `json:"total"`
	Count int    `json:"count"`
}

// subcategoryTotalDTO keeps the category and the subcategory apart. The
// dashboard shows them joined as «Категория → Подкатегория», but that arrow is
// a label and belongs to the view, not to the wire.
type subcategoryTotalDTO struct {
	Category    string `json:"category"`
	Subcategory string `json:"subcategory"`
	Total       string `json:"total"`
	Count       int    `json:"count"`
}

// periodTotalDTO is one calendar period: YYYY-MM for months, YYYY-MM-DD for
// days. Only periods that actually have expenses appear — filling the gaps needs
// a window, and the window belongs to whichever chart is drawing it.
type periodTotalDTO struct {
	Period string `json:"period"`
	Total  string `json:"total"`
	Count  int    `json:"count"`
}

// financeSummaryDTO is the finance view's arithmetic, done once, on the server.
//
// Every list is present even when empty: a missing field is indistinguishable
// from "no data" in the client, and the chart would quietly disappear instead of
// rendering an empty state.
type financeSummaryDTO struct {
	ExpenseCount   int                   `json:"expenseCount"`
	Expenses       string                `json:"expenses"`
	IncomeCount    int                   `json:"incomeCount"`
	Income         string                `json:"income"`
	Net            string                `json:"net"`
	ByCategory     []namedTotalDTO       `json:"byCategory"`
	ByAccount      []namedTotalDTO       `json:"byAccount"`
	ByPlace        []namedTotalDTO       `json:"byPlace"`
	BySource       []namedTotalDTO       `json:"bySource"`
	IncomeBySource []namedTotalDTO       `json:"incomeBySource"`
	BySubcategory  []subcategoryTotalDTO `json:"bySubcategory"`
	ByMonth        []periodTotalDTO      `json:"byMonth"`
	ByDay          []periodTotalDTO      `json:"byDay"`
}

func toNamedTotals(in []finance.CategoryTotal) []namedTotalDTO {
	out := make([]namedTotalDTO, 0, len(in))
	for _, c := range in {
		out = append(out, namedTotalDTO{Name: c.Category, Total: c.Total.String(), Count: c.Count})
	}
	return out
}

func toFinanceSummaryDTO(s finance.Summary) financeSummaryDTO {
	dto := financeSummaryDTO{
		ExpenseCount:   s.ExpenseCount,
		Expenses:       s.Expenses.String(),
		IncomeCount:    s.IncomeCount,
		Income:         s.Income.String(),
		Net:            s.Net.String(),
		ByCategory:     toNamedTotals(s.ByCategory),
		ByAccount:      toNamedTotals(s.ByAccount),
		ByPlace:        toNamedTotals(s.ByPlace),
		BySource:       toNamedTotals(s.BySource),
		IncomeBySource: toNamedTotals(s.IncomeBySource),
		BySubcategory:  make([]subcategoryTotalDTO, 0, len(s.BySubcategory)),
		ByMonth:        make([]periodTotalDTO, 0, len(s.ByMonth)),
		ByDay:          make([]periodTotalDTO, 0, len(s.ByDay)),
	}
	for _, c := range s.BySubcategory {
		dto.BySubcategory = append(dto.BySubcategory, subcategoryTotalDTO{
			Category: c.Category, Subcategory: c.Subcategory,
			Total: c.Total.String(), Count: c.Count,
		})
	}
	for _, m := range s.ByMonth {
		dto.ByMonth = append(dto.ByMonth, periodTotalDTO{Period: m.Month, Total: m.Total.String(), Count: m.Count})
	}
	for _, d := range s.ByDay {
		dto.ByDay = append(dto.ByDay, periodTotalDTO{Period: d.Date, Total: d.Total.String(), Count: d.Count})
	}
	return dto
}

// accountDTO is one balance from the workbook's «Счета» sheet.
type accountDTO struct {
	Bank    string `json:"bank"`
	Balance string `json:"balance"`
	Updated string `json:"updated"`
	// Current — остаток на сейчас: подтверждённое число минус траты, записанные
	// после подтверждения. Spent — сколько именно ушло. Оба считает usecase, а
	// не страница: иначе веб и терминал разошлись бы в арифметике молча.
	Current           string `json:"current"`
	Spent             string `json:"spent"`
	NeedsConfirmation bool   `json:"needs_confirmation"`
	// Group — род счёта, прочитанный из имени («Заморозка → Хранение» →
	// «Заморозка»), пустой у обычного счёта. Имя разбирает домен, а не
	// страница: разбирая его сама, каждая витрина однажды разберёт иначе.
	Group string `json:"group,omitempty"`
	// NameInGroup — как счёт зовётся внутри своего рода. Отдаётся отдельно,
	// потому что заголовок рода уже написан над строкой, и повторять его в
	// строке значит тратить ширину на прочитанное.
	NameInGroup string `json:"name_in_group,omitempty"`
}

func toTransactionDTO(t domain.Transaction) transactionDTO {
	return transactionDTO{
		ID:          t.ID(),
		Kind:        t.Kind(),
		Date:        t.Date().Format(time.DateOnly),
		Amount:      t.Amount().String(),
		Category:    t.Category(),
		Subcategory: t.Subcategory(),
		Place:       t.Place(),
		Description: t.Description(),
		Account:     t.Account(),
		Source:      t.Source(),
		RecordedAt:  recordedAtOrEmpty(t.ID()),
	}
}

// recordedAtOrEmpty отдаёт момент записи строки, а пустую строку — когда он
// неизвестен. Правило одно на движок и живёт в домене: витрина, разбирая id
// сама, однажды разберёт иначе — так уже было, регулярка фронта и разбор в Go
// расходились на строчных буквах и на переполнении метки.
//
// Точность — МИЛЛИСЕКУНДЫ, и это не украшение формата. ULID несёт метку с
// точностью до миллисекунды, а RFC3339 её отбрасывает: две строки, записанные
// в одну секунду, приезжали на витрину с одинаковым моментом. Фронт видел
// равенство, устойчивая сортировка оставляла их в порядке файла, а терминал в
// это же время сравнивал полные миллисекунды и давал ОБРАТНЫЙ порядок. На
// живой книге так расходились 10 строк в 4 корзинах.
//
// Формат подобран так, чтобы строковое сравнение совпадало с временным:
// фиксированное число знаков после точки и всегда UTC. RFC3339Nano не годится —
// он срезает конечные нули, и "…45.870Z" оказалось бы длиннее "…45.12Z" при
// том, что 120 мс раньше 870 мс.
func recordedAtOrEmpty(id string) string {
	at, ok := domain.RecordedAt(id)
	if !ok {
		return ""
	}
	return at.UTC().Format("2006-01-02T15:04:05.000Z")
}

// toBalanceDTO собирает счёт вместе с расчётом остатка.
func toBalanceDTO(b finance.AccountBalance) accountDTO {
	return accountDTO{
		Bank:              b.Bank,
		Balance:           b.Confirmed.String(),
		Updated:           b.ConfirmedOn,
		Current:           b.Current.String(),
		Spent:             b.Spent.String(),
		NeedsConfirmation: b.NeedsConfirmation,
		Group:             b.Group,
		NameInGroup:       b.NameWithinGroup,
	}
}

// entryDTO is the JSON shape of an entry exposed by the API. Optional aspects
// are omitted when absent.
type entryDTO struct {
	ID            int      `json:"id"`
	HabrID        *int     `json:"habr_id,omitempty"`
	Title         string   `json:"title"`
	URL           string   `json:"url,omitempty"`
	Category      string   `json:"category"`
	Kind          string   `json:"kind"`
	Lifecycle     string   `json:"lifecycle"`
	Verdict       *string  `json:"verdict,omitempty"`
	ReadState     *string  `json:"read_state,omitempty"`
	PublishStage  *string  `json:"publish_stage,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	DateAdded     string   `json:"date_added,omitempty"`
	DateCreated   string   `json:"date_created,omitempty"`
	Description   string   `json:"description,omitempty"`
	Author        string   `json:"author,omitempty"`
	Source        string   `json:"source,omitempty"`
	IsTranslation bool     `json:"is_translation,omitempty"`
	// Путь к собственному тексту записи и ссылки на связанные записи. После
	// ADR-0004 вдвоём они и есть связь «статья → её разбор»: файл принадлежит
	// той записи, чей он, а цитирующие статьи держат related_ids. Оба поля
	// опускаются пустыми — «разбора нет» и «разбор по пустому пути» на экране
	// выглядели бы одинаково.
	// Дата выхода у автора и дата глубокого разбора. Обе опускаются пустыми:
	// «даты нет» и «дата пустая» на экране выглядели бы одинаково.
	HabrDate     string `json:"habr_date,omitempty"`
	DeepReadDate string `json:"deep_read_date,omitempty"`
	File         string `json:"file,omitempty"`
	RelatedIDs   []int  `json:"related_ids,omitempty"`
}

func toDTO(e domain.Entry) entryDTO {
	d := entryDTO{
		ID:            e.ID(),
		HabrID:        e.HabrID(),
		Title:         e.Title(),
		URL:           e.URL(),
		Category:      e.Category().String(),
		Kind:          e.Kind(),
		Lifecycle:     e.Lifecycle().String(),
		Tags:          e.Tags(),
		Description:   e.Description(),
		Author:        e.Author(),
		Source:        e.Source(),
		IsTranslation: e.IsTranslation(),
		File:          e.NotesFile(),
		RelatedIDs:    e.RelatedIDs(),
	}
	if v := e.Verdict(); v != nil {
		s := v.String()
		d.Verdict = &s
	}
	if r := e.ReadState(); r != nil {
		s := r.String()
		d.ReadState = &s
	}
	if p := e.PublishStage(); p != nil {
		s := p.String()
		d.PublishStage = &s
	}
	// Оба поля, а не одно: они почти не пересекаются, и запись обычно несёт
	// ровно одно из двух. Какое показывать — решает вид, но выбирать он может
	// только из того, что доехало.
	if t := e.DateAdded(); t != nil {
		d.DateAdded = t.Format(time.DateOnly)
	}
	if t := e.DateCreated(); t != nil {
		d.DateCreated = t.Format(time.DateOnly)
	}
	if t := e.HabrDate(); t != nil {
		d.HabrDate = t.Format(time.DateOnly)
	}
	if t := e.DeepReadDate(); t != nil {
		d.DeepReadDate = t.Format(time.DateOnly)
	}
	return d
}

// freshnessDTO — ответ на вопрос «не отстала ли страница».
//
// Behind и Unknown — разные вещи, и обе едут наружу: «не знаю, когда правили»
// нельзя показывать как «всё в порядке», иначе страница выглядит проверенной,
// не будучи ею.
type freshnessDTO struct {
	Behind   bool            `json:"behind"`
	Unknown  bool            `json:"unknown"`
	EditedAt string          `json:"edited_at,omitempty"`
	Facts    []freshnessFact `json:"facts"`
	Draft    string          `json:"draft,omitempty"`
}

type freshnessFact struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Count int    `json:"count,omitempty"`
	IDs   []int  `json:"ids,omitempty"`
}

func toFreshnessDTO(r freshness.Report) freshnessDTO {
	out := freshnessDTO{
		Behind:  r.Behind,
		Unknown: r.Unknown,
		Draft:   r.Draft,
		// Пустой список, а не null: nil-слайс Go пишет как null, и `.length` у
		// него роняет всё дерево React — этой граблёй уже белел дашборд.
		Facts: []freshnessFact{},
	}
	if !r.EditedAt.IsZero() {
		out.EditedAt = r.EditedAt.Format(time.RFC3339)
	}
	for _, f := range r.Facts {
		out.Facts = append(out.Facts, freshnessFact{Kind: f.Kind, Text: f.Text, Count: f.Count, IDs: f.IDs})
	}
	return out
}

// sourceStateDTO — свежесть одного источника страницы.
type sourceStateDTO struct {
	Name     string `json:"name"`
	Flag     string `json:"flag"`
	EditedAt string `json:"edited_at,omitempty"`
	Behind   bool   `json:"behind"`
	Unknown  bool   `json:"unknown"`
	// NoAnchors — сверять не с чем. Отдельно от behind=false, потому что
	// «проверено, всё хорошо» и «проверять нечем» — разные вещи.
	NoAnchors bool `json:"no_anchors"`
	// StaleBuild — страница называет версию новее собранной: обновлять надо
	// движок, а не файл. Отдельно от behind, потому что чинится другим.
	StaleBuild bool            `json:"stale_build"`
	AgeDays    int             `json:"age_days"`
	Facts      []freshnessFact `json:"facts"`
	Draft      string          `json:"draft,omitempty"`
}

func toSourceDTO(s freshness.SourceState, draft string) sourceStateDTO {
	out := sourceStateDTO{
		Name: s.Name, Flag: s.Flag, Behind: s.Behind, Unknown: s.Unknown,
		NoAnchors: s.NoAnchors, StaleBuild: s.StaleBuild, AgeDays: s.AgeDays, Draft: draft,
		Facts: []freshnessFact{},
	}
	if !s.EditedAt.IsZero() {
		out.EditedAt = s.EditedAt.Format(time.RFC3339)
	}
	for _, f := range s.Facts {
		out.Facts = append(out.Facts, freshnessFact{Kind: f.Kind, Text: f.Text, Count: f.Count, IDs: f.IDs})
	}
	return out
}
