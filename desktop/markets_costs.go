package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/tablefile"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Себестоимость товаров (#580): по артикулу продавца, у каждого кабинета своя,
// в costs.json рядом с настройками окна. Ключей и денег кабинета там нет —
// только то, что ввел человек.

// MarketsImportCosts reads a cost table (xlsx или csv) into cabinet id's costs.
func (a *App) MarketsImportCosts(id, path string) (costImport, error) {
	return a.settings.ImportCosts(id, path)
}

// MarketsSetCost sets one article's cost of cabinet id; "" — убрать.
func (a *App) MarketsSetCost(id, article, value string) (float64, error) {
	return a.settings.SetCost(id, article, value)
}

// costStore — кабинет → артикул → копейки.
type costStore map[string]map[string]int64

func (f *folderSettings) costsPath() string { return filepath.Join(filepath.Dir(f.path), "costs.json") }

// readCosts reads costs.json; нет файла — пусто. Под costMu.
func (f *folderSettings) readCosts() (costStore, error) {
	raw, err := os.ReadFile(f.costsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return costStore{}, nil
	}
	if err != nil {
		return nil, err
	}
	st := costStore{}
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("себестоимость %s: %w", f.costsPath(), err)
	}
	return st, nil
}

// writeCosts saves costs.json через временный файл: обрыв не оставит половину. Под costMu.
func (f *folderSettings) writeCosts(st costStore) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.costsPath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.costsPath())
}

// errNoOzon — кабинета с таким id нет среди подключений Ozon.
var errNoOzon = errors.New("кабинета Ozon нет в «Подключениях» — добавьте его")

// ozonCabinetConn returns the window's Ozon cabinet id: одна проверка для чтения
// кабинета и его себестоимости (ревью #598).
func (f *folderSettings) ozonCabinetConn(id string) (desktopconfig.Connection, error) {
	c, ok := f.snapshot().cfg.Connection(id)
	if !ok || c.Kind != desktopconfig.ConnOzon {
		return c, errNoOzon
	}
	return c, nil
}

// dropCosts forgets a removed cabinet's costs: вновь добавленный не получит чужие.
func (f *folderSettings) dropCosts(id string) error {
	f.costMu.Lock()
	defer f.costMu.Unlock()
	st, err := f.readCosts()
	if err != nil || st[id] == nil {
		return err
	}
	delete(st, id)
	return f.writeCosts(st)
}

// update changes cabinet id's costs under the lock.
func (f *folderSettings) updateCosts(id string, change func(map[string]int64)) error {
	if _, err := f.ozonCabinetConn(id); err != nil {
		return err
	}
	f.costMu.Lock()
	defer f.costMu.Unlock()
	st, err := f.readCosts()
	if err != nil {
		return err
	}
	if st[id] == nil {
		st[id] = map[string]int64{}
	}
	change(st[id])
	return f.writeCosts(st)
}

// ImportCosts reads a cost table into cabinet id's costs: артикулы из файла
// заменяют прежние значения, остальные остаются.
func (f *folderSettings) ImportCosts(id, path string) (costImport, error) {
	rows, err := tablefile.Read(path)
	if err != nil {
		return costImport{}, err
	}
	costs, probs, err := markets.ReadCosts(rows)
	if err != nil {
		return costImport{}, err
	}
	err = f.updateCosts(id, func(m map[string]int64) {
		for a, c := range costs {
			m[a] = c.Kopecks()
		}
	})
	out := costImport{Read: len(costs), Problems: []nicheProblem{}, Costs: map[string]float64{}}
	for a, c := range costs {
		out.Costs[a] = float64(c.Kopecks()) / 100 // окно пересчитывает прибыль на месте, без новой загрузки кабинета
	}
	for _, p := range probs {
		out.Problems = append(out.Problems, nicheProblem{Row: p.Row, Reason: p.Reason})
	}
	return out, err
}

// SetCost sets one article's cost of cabinet id; "" — убрать. Отдает
// сохраненное в рублях: окно считает по нему, а не по своему разбору ввода.
func (f *folderSettings) SetCost(id, article, value string) (float64, error) {
	article = strings.TrimSpace(article)
	if article == "" {
		return 0, markets.ErrNoArticle
	}
	if strings.TrimSpace(value) == "" {
		return 0, f.updateCosts(id, func(m map[string]int64) { delete(m, article) })
	}
	c, err := markets.ParseCost(article, value)
	if err != nil {
		return 0, err
	}
	return float64(c.Kopecks()) / 100, f.updateCosts(id, func(m map[string]int64) { m[article] = c.Kopecks() })
}

// costsOf returns cabinet id's costs by article.
func (f *folderSettings) costsOf(id string) (map[string]domain.Money, error) {
	f.costMu.Lock()
	defer f.costMu.Unlock()
	st, err := f.readCosts()
	if err != nil {
		return nil, err
	}
	out := map[string]domain.Money{}
	for a, k := range st[id] {
		out[a] = domain.NewMoney(k)
	}
	return out, nil
}

// rowsWithMoney fills the rows' money from accruals: на счет и прибыль до рекламы.
func rowsWithMoney(v *shopView, acc []markets.OzonAccrual, costs map[string]domain.Money) {
	article := map[string]string{}
	for _, p := range v.Products {
		article[p.SKU] = p.Article
	}
	at := map[string]markets.SKUEconomy{}
	for _, e := range markets.OzonSKUEconomy(acc, func(sku string) (domain.Money, bool) {
		c, ok := costs[article[sku]]
		return c, ok
	}) {
		at[e.SKU] = e
	}
	rub := func(m domain.Money) float64 { return float64(m.Kopecks()) / 100 }
	for i, p := range v.Products {
		e, ok := at[p.SKU]
		if !ok {
			if c, has := costs[p.Article]; has { // без начислений — себестоимость все равно видна в карточке
				v.Products[i].Money = &rowMoney{UnitCost: rub(c), HasCost: true}
			}
			continue
		}
		m := &rowMoney{Units: e.Units, Sales: rub(e.Sales), Commission: rub(e.Commission), Delivery: rub(e.Delivery), Partners: rub(e.Partners),
			Other: rub(e.Other.Add(e.Promotion)), Total: rub(e.Total), UnitCost: rub(e.UnitCost), HasCost: e.HasCost, Cost: rub(e.Cost())}
		v.Products[i].Money, v.Products[i].OnAccount = m, m.Total
		if e.HasCost {
			pr := rub(e.Profit())
			v.Products[i].Profit = &pr
		}
	}
}
