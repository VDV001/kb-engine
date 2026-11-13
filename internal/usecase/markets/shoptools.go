package markets

import (
	"context"
	"time"
)

// ShopData is not written yet.
type ShopData struct {
	Name     string
	Posts    []OzonPosting
	Stock    []OzonStock
	From, To time.Time
	Loc      *time.Location
}

// ShopToolSpec is not written yet.
type ShopToolSpec struct{ Name, Description, Params string }

// ShopTools is not written yet.
type ShopTools struct {
	Load func(ctx context.Context) (ShopData, error)
}

// Specs is not written yet.
func (ShopTools) Specs() []ShopToolSpec { return nil }

// Run is not written yet.
func (t ShopTools) Run(ctx context.Context, name string, args []byte) (string, error) { return "", nil }
