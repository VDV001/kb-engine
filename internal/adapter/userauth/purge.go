package userauth

import (
	"context"
	"errors"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNoPurge: the store cannot remove a tree (no blob.Purger).
var ErrNoPurge = errors.New("userauth: the store cannot purge")

// PurgeReport counts the objects Purge took, by place.
type PurgeReport struct {
	Base int // users/<id>/: база, финансы, журнал ящика
	Mail int // mailfiles/users/<id>/: файлы ящика
	Seen int // auth/seen/<id>.json: входы телефонов
}

// Purge stub, filled in by the next commit.
func Purge(ctx context.Context, s blob.Store, id domain.UserID) (PurgeReport, error) {
	return PurgeReport{}, nil
}
