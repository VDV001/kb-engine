package main

import (
	"errors"
	"path/filepath"

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
}

type nicheRow struct {
	Name   string             `json:"name"`
	Values map[string]float64 `json:"values"`
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
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Выгрузка ниш из сервиса аналитики",
		Filters: []runtime.FileFilter{{DisplayName: "Таблицы (xlsx, csv)", Pattern: "*.xlsx;*.csv"}},
	})
}

// MarketsRead reads a niche export with the person's picks.
func (a *App) MarketsRead(path string, picks []nichePick) (nicheTable, error) {
	return readNicheFile(path, picks)
}

func readNicheFile(path string, picks []nichePick) (nicheTable, error) {
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
		out.Columns = append(out.Columns, nicheColumn{Index: c.Index, Header: c.Header, Field: string(c.Field)})
	}
	for _, n := range tb.Niches {
		row := nicheRow{Name: n.Name(), Values: map[string]float64{}}
		for _, c := range domain.NicheColumns() {
			if v, ok := n.Value(c); ok {
				row.Values[string(c)] = v
			}
		}
		out.Niches = append(out.Niches, row)
	}
	for _, p := range tb.Problems {
		out.Problems = append(out.Problems, nicheProblem{Row: p.Row, Reason: p.Reason})
	}
	return out, nil
}
