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

// compressChunkRunes — сколько знаков истории уходит модели за один вызов.
// Сжимают как раз то, что не лезет в контекст, поэтому историю нельзя слать
// одним запросом: она кусками, а конспект прошлого куска едет в следующий.
const compressChunkRunes = 24000

// Compress asks the model to fold all but the last keep messages into a summary.
func (s *Service) Compress(ctx context.Context, history []Message, keep int) (Compression, error) {
	// Хвост не пуст всегда: за конспектом идет новый вопрос, и два сообщения
	// человека подряд провайдеры с чередованием ролей не принимают.
	folded := len(history) - max(keep, 1)
	if folded <= 0 {
		return Compression{}, ErrNothingToCompress
	}
	summary := ""
	for _, chunk := range chunks(history[:folded], compressChunkRunes) {
		content := chunk
		if summary != "" {
			content = "Конспект предыдущей части:\n" + summary + "\n\nПродолжение разговора:\n" + chunk
		}
		reply, err := s.model.Complete(ctx, []ModelMessage{
			{Role: RoleSystem, Content: compressPrompt},
			{Role: RoleUser, Content: content},
		}, nil)
		if err != nil {
			return Compression{}, fmt.Errorf("%w: %w", ErrModel, err)
		}
		if summary = strings.TrimSpace(reply.Content); summary == "" {
			return Compression{}, fmt.Errorf("%w: empty summary", ErrModel)
		}
	}
	return Compression{Text: summary, Folded: folded}, nil
}

// chunks renders messages as «роль: текст» and groups them up to limit runes;
// сообщение длиннее предела режется на части, а не обрезается: хвост длинного
// ответа не должен пропасть из конспекта вместе с исходником в запросе.
func chunks(msgs []Message, limit int) []string {
	var out []string
	var cur strings.Builder
	n := 0
	flush := func() {
		if n > 0 {
			out = append(out, cur.String())
			cur.Reset()
			n = 0
		}
	}
	for _, m := range msgs {
		text := []rune(m.Text)
		for part := 0; part == 0 || len(text) > 0; part++ {
			take := min(len(text), limit-100)
			label := string(m.Role)
			if part > 0 {
				label += " (продолжение)"
			}
			line := fmt.Sprintf("%s: %s\n\n", label, string(text[:take]))
			text = text[take:]
			if size := len([]rune(line)); n > 0 && n+size > limit {
				flush()
			}
			cur.WriteString(line)
			n += len([]rune(line))
		}
	}
	flush()
	return out
}

// Apply returns the history the model sees: конспект вместо свернутых сообщений
// и хвост как есть. Исходный срез не меняется. Конспект идет от человека; если
// хвост начинается с сообщения человека, они сливаются в одно: провайдеры с
// чередованием ролей не принимают два сообщения человека подряд.
func (c Compression) Apply(history []Message) []Message {
	if c.Folded <= 0 || c.Folded >= len(history) {
		return history
	}
	note := fmt.Sprintf("Конспект начала разговора (сжато %d сообщ.):\n%s", c.Folded, c.Text)
	tail := history[c.Folded:]
	out := make([]Message, 0, len(tail)+1)
	if len(tail) > 0 && tail[0].Role == RoleUser {
		out = append(out, Message{Role: RoleUser, Text: note + "\n\n" + tail[0].Text})
		return append(out, tail[1:]...)
	}
	out = append(out, Message{Role: RoleUser, Text: note})
	return append(out, tail...)
}
