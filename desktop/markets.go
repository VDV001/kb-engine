package main

import (
	"errors"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/daniil/kb-engine/internal/adapter/tablefile"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// «Маркеты» (#580): экран «Ниша» читает выгрузку ниш из сервиса аналитики.
// Файл читается при каждом открытии и выборе колонок; окно его не копирует.

// nicheTable — прочитанный файл ниш для окна. NeedName — колонку с названием
// ниши не узнал: окно спрашивает, какая из колонок ниша.
type nicheTable struct {
	Path      string         `json:"path"`
	File      string         `json:"file"`
	HeaderRow int            `json:"headerRow"`
	NeedName  bool           `json:"needName"`
	Fields    []nicheField   `json:"fields"`
	Columns   []nicheColumn  `json:"columns"`
	Niches    []nicheRow     `json:"niches"`
	Problems  []nicheProblem `json:"problems"`
}

type nicheField struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type nicheColumn struct {
	Index  int    `json:"index"`
	Header string `json:"header"`
	Field  string `json:"field"`
	Month  int    `json:"month"` // 1-12 — продажи за месяц; 0 — не месяц
	Year   int    `json:"year"`
}

type nicheRow struct {
	Name    string             `json:"name"`
	Values  map[string]float64 `json:"values"`
	Months  []nicheMonth       `json:"months"`
	Markets []string           `json:"markets"`
	Score   nicheScore         `json:"score"`
}

type nicheMonth struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

// nicheScore — оценка 0-100 и ее части; Parts — вклад каждой части с весом,
// чтобы подсказка объясняла число, а не повторяла его.
type nicheScore struct {
	Ok        bool        `json:"ok"`
	Score     int         `json:"score"`
	Missing   []string    `json:"missing"`
	Parts     []scorePart `json:"parts"`
	PerSeller float64     `json:"perSeller"`
	Growth    string      `json:"growth"`
	Crowded   bool        `json:"crowded"`
	Peak      int         `json:"peak"` // 1-12; 0 — пика нет
	InSeason  bool        `json:"inSeason"`
	WeeksFrom int         `json:"weeksFrom"`
	WeeksTo   int         `json:"weeksTo"`
}

type scorePart struct {
	ID     string  `json:"id"`
	Weight float64 `json:"weight"`
	Value  float64 `json:"value"` // 0..1
}

type nicheProblem struct {
	Row    int    `json:"row"`
	Reason string `json:"reason"`
}

// nichePick — выбор человека: колонка и величина в ней, "" — не нужна.
type nichePick struct {
	Index int    `json:"index"`
	Field string `json:"field"`
}

// MarketsPickFile asks for a niche export; "" — передумали.
func (a *App) MarketsPickFile() (string, error) {
	return a.MarketsPickFileTitled("Выгрузка ниш из сервиса аналитики")
}

// MarketsPickFileTitled asks for an xlsx or csv with the dialog's title.
func (a *App) MarketsPickFileTitled(title string) (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   title,
		Filters: []runtime.FileFilter{{DisplayName: "Таблицы (xlsx, csv)", Pattern: "*.xlsx;*.csv"}},
	})
}

// MarketsRead reads a niche export with the person's picks.
func (a *App) MarketsRead(path string, picks []nichePick) (nicheTable, error) {
	return readNicheFile(path, picks, time.Now())
}

func readNicheFile(path string, picks []nichePick, now time.Time) (nicheTable, error) {
	rows, err := tablefile.Read(path)
	if err != nil {
		return nicheTable{}, err
	}
	byIndex := make(map[int]domain.NicheColumn, len(picks))
	for _, p := range picks {
		byIndex[p.Index] = domain.NicheColumn(p.Field)
	}
	tb, err := markets.ReadNiches(rows, byIndex)
	needName := errors.Is(err, markets.ErrNoNameColumn)
	if err != nil && !needName {
		return nicheTable{}, err
	}
	out := nicheTable{Path: path, File: filepath.Base(path), HeaderRow: tb.HeaderRow, NeedName: needName,
		Fields: []nicheField{}, Columns: []nicheColumn{}, Niches: []nicheRow{}, Problems: []nicheProblem{}}
	for _, c := range domain.NicheColumns() {
		out.Fields = append(out.Fields, nicheField{ID: string(c), Label: c.Label()})
	}
	for _, c := range tb.Columns {
		out.Columns = append(out.Columns, nicheColumn{Index: c.Index, Header: c.Header, Field: string(c.Field), Month: int(c.Month), Year: c.Year})
	}
	scores := domain.ScoreNiches(tb.Niches, now)
	for i, n := range tb.Niches {
		out.Niches = append(out.Niches, nicheRowOf(n, scores[i]))
	}
	for _, p := range tb.Problems {
		out.Problems = append(out.Problems, nicheProblem{Row: p.Row, Reason: p.Reason})
	}
	return out, nil
}

// monthShort — подписи месяцев в графике сезона.
var monthShort = [...]string{"", "янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}

func nicheRowOf(n *domain.Niche, sc domain.NicheScore) nicheRow {
	row := nicheRow{Name: n.Name(), Values: map[string]float64{}, Months: []nicheMonth{}, Markets: []string{}}
	for _, c := range domain.NicheColumns() {
		if v, ok := n.Value(c); ok {
			row.Values[string(c)] = v
		}
	}
	for _, m := range n.Months() {
		label := monthShort[m.Month]
		if m.Year > 0 {
			label += " " + strconv.Itoa(m.Year)
		}
		row.Months = append(row.Months, nicheMonth{Label: label, Value: m.Value})
	}
	for _, m := range n.Marketplaces() {
		row.Markets = append(row.Markets, string(m))
	}
	row.Score = nicheScore{Ok: sc.Ok, Score: sc.Score, Missing: []string{}, Parts: []scorePart{}, PerSeller: sc.PerSeller,
		Growth: string(sc.Growth), Crowded: sc.Crowded, InSeason: sc.InSeason, WeeksFrom: sc.WeeksFrom, WeeksTo: sc.WeeksTo}
	if sc.HasPeak {
		row.Score.Peak = int(sc.Peak)
	}
	for _, m := range sc.Missing {
		row.Score.Missing = append(row.Score.Missing, string(m))
	}
	for _, p := range sc.Parts {
		row.Score.Parts = append(row.Score.Parts, scorePart{ID: string(p.Part), Weight: p.Weight, Value: p.Value})
	}
	return row
}
