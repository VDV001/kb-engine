package chat

import "context"

// Extra is not written yet.
type Extra interface {
	Prompt() string
	Tools() []Tool
	Exec(ctx context.Context, name string, args []byte) (string, bool)
}
