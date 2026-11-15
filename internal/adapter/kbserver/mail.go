package kbserver

import (
	"context"
	"errors"
	"time"
)

// ErrNoMailbox — у сервера нет ящика чата с телефона.
var ErrNoMailbox = errors.New("kbserver: no mailbox")

// Phone is a paired phone.
type Phone struct {
	Device string
	At     time.Time
}

// AddPhone pairs a phone.
func (c Client) AddPhone(ctx context.Context) (string, string, error) { return "", "", nil }

// Phones lists paired phones.
func (c Client) Phones(ctx context.Context) ([]Phone, error) { return nil, nil }

// RevokePhone unpairs a phone.
func (c Client) RevokePhone(ctx context.Context, device string) error { return nil }
