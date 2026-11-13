package main

import "github.com/daniil/kb-engine/internal/domain"

// ImportCosts reads a cost table into cabinet id's costs.
func (f *folderSettings) ImportCosts(id, path string) (costImport, error) { return costImport{}, nil }

// SetCost sets one article's cost of cabinet id; "" — убрать.
func (f *folderSettings) SetCost(id, article, value string) error { return nil }

// costsOf returns cabinet id's costs by article.
func (f *folderSettings) costsOf(id string) (map[string]domain.Money, error) { return nil, nil }
