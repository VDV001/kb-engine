package catalogjson

import (
	"context"
	"errors"

	"github.com/daniil/kb-engine/internal/adapter/blob"
)

func SetFieldsDoc([]byte, []int, Changes) ([]byte, int, error) {
	return nil, 0, errors.New("not implemented")
}

type StoreWriter struct{}

func NewStoreWriter(blob.Store, string) StoreWriter { return StoreWriter{} }

func (StoreWriter) Update(context.Context, func([]byte) ([]byte, error)) error {
	return errors.New("not implemented")
}
