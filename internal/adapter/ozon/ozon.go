// Package ozon reads a seller's cabinet through Ozon Seller API (#580):
// только чтение — отправления и остатки.
package ozon

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// Ошибки API.
var (
	ErrUnauthorized = errors.New("Ozon не принял Client-Id или ключ")
	ErrInsecureURL  = errors.New("ключ Ozon уходит только по https")
)

// Client is not written yet.
type Client struct {
	BaseURL, ClientID, Key string
	HTTP                   *http.Client
}

// Postings is not written yet.
func (c Client) Postings(ctx context.Context, from, to time.Time) ([]markets.OzonPosting, error) {
	return nil, nil
}

// Stocks is not written yet.
func (c Client) Stocks(ctx context.Context) ([]markets.OzonStock, error) { return nil, nil }
