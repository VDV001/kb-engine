package ghrelease

import (
	"context"
	"errors"
	"net/http"
)

var (
	ErrTooLarge = errors.New("release file is over the limit")
	ErrBadPath  = errors.New("release path is not valid")
)

type Client struct {
	Base string
	HTTP *http.Client
}

func (c Client) Fetch(context.Context, string, int64) ([]byte, error) { return nil, nil }
