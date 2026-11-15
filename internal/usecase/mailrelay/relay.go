// Package mailrelay answers the phone from the desk (#622, этап 3). Заготовка.
package mailrelay

import (
	"context"

	"github.com/daniil/kb-engine/internal/domain"
)

// Message is one sealed message of the mailbox.
type Message struct {
	Seq    int64
	Chat   string
	From   domain.MailSide
	Sealed []byte
}

// Page is a page of the mailbox.
type Page struct {
	Messages []Message
	More     bool
}

// Opened is a message that passed the check.
type Opened struct {
	Sender  string
	Counter uint64
	Plain   []byte
}

// Payload is the text inside the seal.
type Payload struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Kinds of payload.
const (
	KindQuestion = "question"
	KindAnswer   = "answer"
	KindError    = "error"
)

// Box is the mailbox.
type Box interface {
	Since(ctx context.Context, after int64) (Page, error)
	Post(ctx context.Context, chat string, sealed []byte) (int64, error)
}

// Crypto opens and seals.
type Crypto interface {
	Open(chat string, from domain.MailSide, sealed []byte) (Opened, error)
	Seal(chat string, counter uint64, plain []byte) ([]byte, error)
}

// Asker runs a model turn.
type Asker interface {
	Ask(ctx context.Context, chat, question string) (string, string, error)
}

// Outgoing is a reply waiting to be posted.
type Outgoing struct {
	Chat    string  `json:"chat"`
	Counter uint64  `json:"counter"`
	Payload Payload `json:"payload"`
}

// State is what the relay remembers.
type State struct {
	After       int64             `json:"after"`
	DeskCounter uint64            `json:"desk_counter"`
	Seen        map[string]uint64 `json:"seen"`
	Chats       map[string]string `json:"chats"`
	Outbox      []Outgoing        `json:"outbox"`
}

// Store keeps the state.
type Store interface {
	Load() (State, error)
	Save(State) error
}

// Relay answers the phone.
type Relay struct {
	Box    Box
	Crypto Crypto
	Asker  Asker
	Store  Store
}

// Tick processes the mailbox once.
func (r Relay) Tick(context.Context) error { return nil }
