package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/chat"
)

func talk(n int) []chat.Message {
	var h []chat.Message
	for i := range n {
		role := chat.RoleUser
		if i%2 == 1 {
			role = chat.RoleAssistant
		}
		h = append(h, chat.Message{Role: role, Text: "реплика " + string(rune('A'+i))})
	}
	return h
}

// Сжатие (#539): модель получает только старую часть, хвост из keep последних
// сообщений остается живым, а число свернутых называется явно.
func TestCompress_FoldsOnlyTheOldPart(t *testing.T) {
	m := &fakeModel{reply: chat.ModelReply{Content: "Конспект: говорили про поставку."}}
	svc := chat.NewService(&fakeSearch{}, m)

	c, err := svc.Compress(context.Background(), talk(10), 4)
	if err != nil {
		t.Fatal(err)
	}
	if c.Text != "Конспект: говорили про поставку." || c.Folded != 6 {
		t.Fatalf("compression = %+v, want folded 6", c)
	}
	var asked strings.Builder
	for _, msg := range m.got {
		asked.WriteString(msg.Content + "\n")
	}
	if !strings.Contains(asked.String(), "реплика A") || !strings.Contains(asked.String(), "реплика F") {
		t.Errorf("модель не увидела старую часть:\n%s", asked.String())
	}
	if strings.Contains(asked.String(), "реплика G") {
		t.Errorf("хвост ушел в сжатие:\n%s", asked.String())
	}
}

func TestCompress_Refusals(t *testing.T) {
	tests := []struct {
		name    string
		history []chat.Message
		keep    int
		model   *fakeModel
		want    error
	}{
		{"история не длиннее хвоста", talk(4), 4, &fakeModel{}, chat.ErrNothingToCompress},
		{"пустая история", nil, 4, &fakeModel{}, chat.ErrNothingToCompress},
		{"модель упала", talk(10), 4, &fakeModel{err: errors.New("boom")}, chat.ErrModel},
		{"пустой конспект", talk(10), 4, &fakeModel{reply: chat.ModelReply{Content: "  "}}, chat.ErrModel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := chat.NewService(&fakeSearch{}, tt.model).Compress(context.Background(), tt.history, tt.keep)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// Apply не меняет исходную историю: сжатое место помечено, файл чата цел.
func TestCompression_ApplyKeepsOriginal(t *testing.T) {
	h := talk(10)
	got := chat.Compression{Text: "Конспект", Folded: 6}.Apply(h)
	if len(got) != 5 {
		t.Fatalf("len = %d, want конспект + 4 хвоста: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Text, "Конспект") || got[1].Text != "реплика G" || got[4].Text != "реплика J" {
		t.Fatalf("history = %+v", got)
	}
	if len(h) != 10 || h[0].Text != "реплика A" {
		t.Fatalf("исходная история изменена: %+v", h)
	}
}
