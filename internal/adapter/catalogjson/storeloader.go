package catalogjson

import (
	"context"
	"errors"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/domain"
)

type StoreLoader struct{}

func NewStoreLoader(context.Context, blob.Store, string) *StoreLoader { return &StoreLoader{} }

func (*StoreLoader) Load() (*domain.Catalog, error) { return nil, errors.New("not implemented") }
