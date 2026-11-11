package chat

import (
	"context"
	"errors"
)

// ErrNothingToCompress — в истории нечего сворачивать: она не длиннее хвоста.
var ErrNothingToCompress = errors.New("nothing to compress")

// Compression is a summary of the old part of a conversation: Text заменяет
// первые Folded сообщений, исходная история остается у вызывающего (#539).
type Compression struct {
	Text   string
	Folded int
}

// Compress asks the model to fold all but the last keep messages into a summary.
func (s *Service) Compress(ctx context.Context, history []Message, keep int) (Compression, error) {
	return Compression{}, errors.New("not implemented")
}

// Apply returns the history the model sees: конспект вместо свернутых сообщений
// и хвост как есть. Исходный срез не меняется.
func (c Compression) Apply(history []Message) []Message { return nil }
