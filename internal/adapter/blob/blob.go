package blob

import (
	"context"
	"errors"
)

var (
	ErrNotFound   = errors.New("blob: key not found")
	ErrConflict   = errors.New("blob: stored version changed")
	ErrInvalidKey = errors.New("blob: invalid key")
	ErrTooLarge   = errors.New("blob: object too large")
)

const MaxObjectSize = 64 << 20

type Object struct {
	Data []byte
	ETag string
}

type Store interface {
	Get(ctx context.Context, key string) (Object, error)
	Put(ctx context.Context, key string, data []byte, ifMatch string) (string, error)
}

func ValidKey(string) error { return nil }
