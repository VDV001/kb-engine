package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/domain"
)

// longChat asks n questions in one chat and returns its id.
func longChat(t *testing.T, d *chatDesk, n int) string {
	t.Helper()
	if err := d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "qwen3"}, ""); err != nil {
		t.Fatal(err)
	}
	id := ""
	for i := range n {
		c, err := d.Ask(askReq{ID: id, Question: "вопрос " + string(rune('A'+i))})
		if err != nil {
			t.Fatal(err)
		}
		id = c.ID
	}
	return id
}

// Сжатие чата (#539): в файле остаются все сообщения, конспект стоит рядом, а
// следующий вопрос модель получает уже с конспектом вместо свернутого начала.
func TestChat_CompressKeepsOriginalAndFeedsSummary(t *testing.T) {
	d, m, _, _ := newTestChatDesk(t)
	id := longChat(t, d, 5) // 10 сообщений
	asked := m.n

	c, err := d.CompressChat(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Compressed == nil || c.Compressed.Folded != 4 || c.Compressed.Text != "ответ [1]" {
		t.Fatalf("compressed = %+v, want folded 4", c.Compressed)
	}
	if len(c.Messages) != 10 {
		t.Fatalf("исходная история тронута: %d сообщений", len(c.Messages))
	}
	if got, _ := d.Get(id); got.Compressed == nil || len(got.Messages) != 10 {
		t.Fatalf("в файле: %+v", got)
	}
	if m.n != asked+1 {
		t.Fatalf("сжатие обязано стоить один вызов модели, было %d", m.n-asked)
	}

	if _, err := d.Ask(askReq{ID: id, Question: "и еще вопрос"}); err != nil {
		t.Fatal(err)
	}
	var seen strings.Builder
	for _, msg := range m.asked[len(m.asked)-1] {
		seen.WriteString(msg.Content + "\n")
	}
	if !strings.Contains(seen.String(), "Конспект начала разговора (сжато 4") {
		t.Errorf("модель не получила конспект:\n%s", seen.String())
	}
	if strings.Contains(seen.String(), "вопрос A") || !strings.Contains(seen.String(), "вопрос E") {
		t.Errorf("свернутое начало ушло в запрос, а хвост пропал:\n%s", seen.String())
	}
}

func TestChat_CompressRefusals(t *testing.T) {
	d, m, _, _ := newTestChatDesk(t)
	short := longChat(t, d, 2) // 4 сообщения: короче хвоста
	asked := m.n
	tests := []struct{ name, id, want string }{
		{"короткий чат", short, "сжимать нечего"},
		{"нет такого файла", "0123456789abcdef", "no such file"},
		{"чужой id", "../x", "нет такого чата"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := d.CompressChat(tt.id)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
	if m.n != asked {
		t.Errorf("отказ не должен звать модель: %d вызовов", m.n-asked)
	}
}

// Сжатие чата — вызов модели, и он виден в «Статистике → Деньги».
func TestChat_CompressIsMetered(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	id := longChat(t, d, 5)
	if _, err := d.CompressChat(id); err != nil {
		t.Fatal(err)
	}
	calls, _, err := d.calls.Load()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range calls {
		if c.Purpose() == domain.CallCompress {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("вызовов сжатия в журнале %d, want 1", n)
	}
}

// Повторное сжатие без новых сообщений ничего не меняет и не платит за вызов
// модели: двойной щелчок или устаревшая страница.
func TestChat_CompressTwiceIsOneCall(t *testing.T) {
	d, m, _, _ := newTestChatDesk(t)
	id := longChat(t, d, 5)
	if _, err := d.CompressChat(id); err != nil {
		t.Fatal(err)
	}
	asked := m.n
	c, err := d.CompressChat(id)
	if err != nil || c.Compressed == nil || c.Compressed.Folded != 4 {
		t.Fatalf("повтор: %+v, %v", c.Compressed, err)
	}
	if m.n != asked {
		t.Fatalf("повторное сжатие позвало модель %d раз(а)", m.n-asked)
	}
}

// Страница узнает, есть ли что сжимать, от окна, а не по своей копии числа
// «шесть»; в файл чата флаг не попадает.
func TestChat_CanCompressComesFromTheWindow(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	short, long := longChat(t, d, 2), longChat(t, d, 5)
	for id, want := range map[string]bool{short: false, long: true} {
		got, err := d.Get(id)
		if err != nil || got.CanCompress != want {
			t.Fatalf("Get(%s).CanCompress = %v, want %v (%v)", id, got.CanCompress, want, err)
		}
		raw, _ := os.ReadFile(filepath.Join(d.dir, id+".json"))
		if strings.Contains(string(raw), "canCompress") {
			t.Errorf("флаг попал в файл чата: %s", raw)
		}
	}
	c, _ := d.Ask(askReq{ID: long, Question: "еще"})
	if !c.CanCompress {
		t.Error("ответ Ask не несет canCompress")
	}
}

// Уже сжатый до хвоста чат сжать нечем: страница не должна звать «Сжать» снова
// (плашка висела бы, а щелчок возвращал бы ту же запись).
func TestChat_CanCompressFalseOnceFoldedToTheTail(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	id := longChat(t, d, 5)
	c, err := d.CompressChat(id)
	if err != nil || c.CanCompress {
		t.Fatalf("после сжатия canCompress = %v, %v", c.CanCompress, err)
	}
	if got, _ := d.Get(id); got.CanCompress {
		t.Error("Get после сжатия обещает, что сжимать есть что")
	}
	c, _ = d.Ask(askReq{ID: id, Question: "еще один"})
	if !c.CanCompress {
		t.Error("появились новые сообщения: сжимать снова есть что")
	}
}

// Флаг «только в ответе»: даже если запись из Get пошла в save, в файл он не
// попадает.
func TestChat_SaveDropsCanCompress(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	id := longChat(t, d, 5)
	rec, err := d.Get(id)
	if err != nil || !rec.CanCompress {
		t.Fatalf("Get: %v %v", rec.CanCompress, err)
	}
	if err := d.save(rec); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(d.dir, id+".json"))
	if strings.Contains(string(raw), "canCompress") {
		t.Fatalf("флаг записан в файл чата: %s", raw)
	}
}
