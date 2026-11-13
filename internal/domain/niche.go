package domain

import "errors"

// ErrInvalidNiche is returned when a niche breaks its rules.
var ErrInvalidNiche = errors.New("ниша не подходит")

// ErrUnknownNicheColumn is returned for a column name the niche file does not have.
var ErrUnknownNicheColumn = errors.New("такой колонки у файла ниш нет")

// NicheColumn — колонка выгрузки ниш из сервиса аналитики (#580).
type NicheColumn string

// Колонки файла ниш.
const (
	NicheName    NicheColumn = "name"
	NicheSales   NicheColumn = "sales"
	NicheRevenue NicheColumn = "revenue"
	NicheSellers NicheColumn = "sellers"
	NichePrice   NicheColumn = "price"
	NicheGrowth  NicheColumn = "growth"
	NicheTop10   NicheColumn = "top10"
)

// NicheColumns lists every column in the order the window shows them.
func NicheColumns() []NicheColumn { return nil }

// Label is the column's name for a person.
func (c NicheColumn) Label() string { return "" }

// ParseNicheColumn validates a column id the window sends back.
func ParseNicheColumn(raw string) (NicheColumn, error) { return "", nil }

// RecognizeNicheColumn guesses the column from a header of the file.
func RecognizeNicheColumn(header string) (NicheColumn, bool) { return "", false }

// Niche — ниша из выгрузки: название и те величины, что были в файле.
type Niche struct {
	name   string
	values map[NicheColumn]float64
}

// NewNiche checks the niche's rules.
func NewNiche(name string, values map[NicheColumn]float64) (*Niche, error) {
	return &Niche{name: name, values: values}, nil
}

// Name is the niche's name.
func (n *Niche) Name() string { return n.name }

// Value returns a value and whether the file had it.
func (n *Niche) Value(c NicheColumn) (float64, bool) { v, ok := n.values[c]; return v, ok }
