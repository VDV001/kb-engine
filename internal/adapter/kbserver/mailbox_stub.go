package kbserver

import (
	"context"
	"time"
)

// MailMessage is one sealed message of the mailbox. Заготовка.
type MailMessage struct {
	Seq    int64     `json:"seq"`
	Chat   string    `json:"chat"`
	From   string    `json:"from"`
	At     time.Time `json:"at"`
	Sealed []byte    `json:"sealed"`
}

// MailPage is an answer of MailSince. Заготовка.
type MailPage struct {
	Messages []MailMessage `json:"messages"`
	First    int64         `json:"first"`
	More     bool          `json:"more"`
}

// MailSince reads the mailbox after seq. Заготовка.
func (c Client) MailSince(context.Context, int64) (MailPage, error) { return MailPage{}, nil }

// MailPost puts a sealed message. Заготовка.
func (c Client) MailPost(context.Context, string, []byte) (int64, error) { return 0, nil }
