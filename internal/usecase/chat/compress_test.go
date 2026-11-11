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

// seqModel answers «С1», «С2»… and remembers every request: сжатие длинной
// истории идет несколькими вызовами.
type seqModel struct {
	n    int
	reqs [][]chat.ModelMessage
}

func (m *seqModel) Complete(_ context.Context, msgs []chat.ModelMessage, _ []chat.Tool) (chat.ModelReply, error) {
	m.n++
	m.reqs = append(m.reqs, msgs)
	return chat.ModelReply{Content: "С" + string(rune('0'+m.n))}, nil
}

func runes(msgs []chat.ModelMessage) int {
	n := 0
	for _, m := range msgs {
		n += len([]rune(m.Content))
	}
	return n
}

// Сжатие нужно именно тогда, когда история не лезет в контекст: свернутая часть
// уходит модели кусками, каждый запрос ограничен, а конспект предыдущего куска
// едет в следующий.
func TestCompress_LongHistoryGoesInChunks(t *testing.T) {
	var h []chat.Message
	for i := range 12 {
		role := chat.RoleUser
		if i%2 == 1 {
			role = chat.RoleAssistant
		}
		h = append(h, chat.Message{Role: role, Text: strings.Repeat("я", 10000)})
	}
	m := &seqModel{}
	c, err := chat.NewService(&fakeSearch{}, m).Compress(context.Background(), h, 2)
	if err != nil {
		t.Fatal(err)
	}
	if m.n < 2 {
		t.Fatalf("100 тыс. знаков одним запросом: вызовов модели %d", m.n)
	}
	for i, r := range m.reqs {
		if got := runes(r); got > 30000 {
			t.Errorf("запрос %d: %d знаков, предел куска 24000 плюс служебное", i, got)
		}
	}
	if !strings.Contains(m.reqs[1][1].Content, "С1") {
		t.Errorf("второй кусок не получил конспект первого:\n%.200s", m.reqs[1][1].Content)
	}
	if c.Text != "С"+string(rune('0'+m.n)) || c.Folded != 10 {
		t.Fatalf("compression = %+v, want последний конспект и folded 10", c)
	}
}

// Одно огромное сообщение не раздувает запрос, но и не теряет хвост: оно
// режется на части, и все знаки доходят до модели.
func TestCompress_OneHugeMessageIsSplitNotCut(t *testing.T) {
	h := append([]chat.Message{{Role: chat.RoleUser, Text: strings.Repeat("я", 200000)}}, talk(6)...)
	m := &seqModel{}
	if _, err := chat.NewService(&fakeSearch{}, m).Compress(context.Background(), h, 2); err != nil {
		t.Fatal(err)
	}
	sent := 0
	for i, r := range m.reqs {
		if got := runes(r); got > 30000 {
			t.Errorf("запрос %d: %d знаков", i, got)
		}
		sent += strings.Count(r[1].Content, "я")
	}
	if sent < 200000 {
		t.Fatalf("до модели дошло %d из 200000 знаков: хвост сообщения потерян", sent)
	}
}

// Сжимать до пустого хвоста нельзя: за конспектом сразу идет новый вопрос, и
// два сообщения человека подряд не принимают провайдеры с чередованием ролей.
func TestCompress_AlwaysKeepsATail(t *testing.T) {
	m := &seqModel{}
	c, err := chat.NewService(&fakeSearch{}, m).Compress(context.Background(), talk(4), 0)
	if err != nil || c.Folded != 3 {
		t.Fatalf("keep=0 обязан оставлять хвост из одного сообщения: %+v, %v", c, err)
	}
	if got := (chat.Compression{Text: "К", Folded: 4}).Apply(talk(4)); len(got) != 4 {
		t.Fatalf("конспект на всю историю без хвоста не применяется: %+v", got)
	}
}

// Apply не меняет исходную историю: сжатое место помечено, файл чата цел.
// Роли не повторяются: провайдеры с чередованием (Anthropic, шаблоны Ollama)
// отвергают два сообщения человека подряд.
func TestCompression_ApplyKeepsOriginal(t *testing.T) {
	h := talk(10)
	got := chat.Compression{Text: "Конспект", Folded: 6}.Apply(h)
	if len(got) != 4 {
		t.Fatalf("len = %d, want конспект слит с первым вопросом хвоста + 3: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Text, "Конспект") || !strings.Contains(got[0].Text, "реплика G") || got[3].Text != "реплика J" {
		t.Fatalf("history = %+v", got)
	}
	if len(h) != 10 || h[0].Text != "реплика A" {
		t.Fatalf("исходная история изменена: %+v", h)
	}
}

func TestCompression_ApplyNeverRepeatsARole(t *testing.T) {
	for folded := 1; folded <= 9; folded++ {
		got := chat.Compression{Text: "К", Folded: folded}.Apply(talk(10))
		for i := 1; i < len(got); i++ {
			if got[i].Role == got[i-1].Role {
				t.Errorf("folded=%d: роль %s подряд на %d-м сообщении: %+v", folded, got[i].Role, i, got)
			}
		}
		if got[0].Role != chat.RoleUser || !strings.Contains(got[0].Text, "К") {
			t.Errorf("folded=%d: начинать надо конспектом от человека: %+v", folded, got[0])
		}
	}
}
