package userauth

import (
	"context"
	"errors"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/domain"
)

const RegistryKey = "auth/users.json"

const RefreshEvery = time.Minute

var (
	ErrUserExists = errors.New("user exists")
	ErrNoSuchUser = errors.New("no such user")
)

func CatalogKey(domain.UserID) string { return "" }

func Add(context.Context, blob.Store, domain.UserID) (string, error) {
	return "", errors.New("not implemented")
}

func Revoke(context.Context, blob.Store, domain.UserID) error { return errors.New("not implemented") }

type Registry struct{}

type Option func(*Registry)

func WithClock(func() time.Time) Option { return func(*Registry) {} }

func NewRegistry(context.Context, blob.Store, ...Option) *Registry { return &Registry{} }

func (*Registry) Lookup(string) (domain.UserID, bool) { return domain.UserID{}, false }
