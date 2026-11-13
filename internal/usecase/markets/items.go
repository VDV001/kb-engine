package markets

import (
	"errors"
	"iter"
)

// ErrNotItems — файл не выгрузка товаров категории.
var ErrNotItems = errors.New("это не выгрузка товаров категории")

// ReadItems is not written yet.
func ReadItems(rows iter.Seq2[[]string, error]) (Table, error) { return Table{}, nil }
