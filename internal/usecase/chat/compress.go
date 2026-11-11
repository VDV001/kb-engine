package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNothingToCompress — в истории нечего сворачивать: она не длиннее хвоста.
var ErrNothingToCompress = errors.New("nothing to compress")

// Compression is a summary of the old part of a conversation: Text заменяет
// первые Folded сообщений, исходная история остается у вызывающего (#539).
type Compression struct {
	Text   string
	Folded int
}

const compressPrompt = `Сожми начало разговора в краткий конспект для себя: о чем спрашивал человек, что выяснили, какие документы и числа назывались, что осталось нерешенным. Пиши от третьего лица, без вступлений, на языке разговора. Ответь только конспектом.`

// Compress asks the model to fold all but the last keep messages into a summary.
func (s *Service) Compress(ctx context.Context, history []Message, keep int) (Compression, error) {
	folded := len(history) - max(keep, 0)
	if folded <= 0 {
		return Compression{}, ErrNothingToCompress
	}
	var b strings.Builder
	for _, m := range history[:folded] {
		fmt.Fprintf(&b, "%s: %s\n\n", m.Role, m.Text)
	}
	reply, err := s.model.Complete(ctx, []ModelMessage{
		{Role: RoleSystem, Content: compressPrompt},
		{Role: RoleUser, Content: b.String()},
	}, nil)
	if err != nil {
		return Compression{}, fmt.Errorf("%w: %w", ErrModel, err)
	}
	text := strings.TrimSpace(reply.Content)
	if text == "" {
		return Compression{}, fmt.Errorf("%w: empty summary", ErrModel)
	}
	return Compression{Text: text, Folded: folded}, nil
}

// Apply returns the history the model sees: конспект вместо свернутых сообщений
// и хвост как есть. Исходный срез не меняется.
func (c Compression) Apply(history []Message) []Message {
	if c.Folded <= 0 || c.Folded > len(history) {
		return history
	}
	out := make([]Message, 0, len(history)-c.Folded+1)
	out = append(out, Message{Role: RoleUser, Text: fmt.Sprintf("Конспект начала разговора (сжато %d сообщ.):\n%s", c.Folded, c.Text)})
	return append(out, history[c.Folded:]...)
}
