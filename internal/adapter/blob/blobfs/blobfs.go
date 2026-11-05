package blobfs

import (
	"context"
	"errors"

	"github.com/daniil/kb-engine/internal/adapter/blob"
)

type Store struct{}

func New(string) (*Store, error) { return &Store{}, nil }

func (*Store) Get(context.Context, string) (blob.Object, error) {
	return blob.Object{}, errors.New("not implemented")
}

func (*Store) Put(context.Context, string, []byte, string) (string, error) {
	return "", errors.New("not implemented")
}
