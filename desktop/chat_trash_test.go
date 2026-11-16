package main

import (
	"context"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// gateModel holds a turn until released: удаление приходит посреди хода.
type gateModel struct{ entered, release chan struct{} }

func (m gateModel) Complete(ctx context.Context, _ []chat.ModelMessage, _ []chat.Tool) (chat.ModelReply, error) {
	close(m.entered)
	select {
	case <-m.release:
	case <-ctx.Done():
		return chat.ModelReply{}, ctx.Err()
	}
	return chat.ModelReply{Content: "ответ"}, nil
}

// Удаление с телефона посреди хода в окне (ревью #662): ход не возвращает
// удаленный чат своим сохранением.
func TestChat_DeleteDuringTurnStaysDeleted(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	_ = d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "x"}, "")
	c, err := d.Ask(askReq{Question: "q"})
	if err != nil {
		t.Fatal(err)
	}
	g := gateModel{entered: make(chan struct{}), release: make(chan struct{})}
	d.model = func(desktopconfig.Chat, string) chat.Model { return g }
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	asked := make(chan error, 1)
	go func() { _, err := d.ask(ctx, askReq{ID: c.ID, Question: "еще"}, askOut{}); asked <- err }()
	select {
	case <-g.entered:
	case <-ctx.Done():
		t.Fatal("ход не начался")
	}
	deleted := make(chan error, 1)
	go func() { deleted <- d.Delete(c.ID) }()
	time.Sleep(50 * time.Millisecond) // удаление успевает прийти, пока ход идет
	close(g.release)
	for _, ch := range []chan error{asked, deleted} {
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatal("ход или удаление повисли")
		}
	}
	if list, _ := d.List(); len(list) != 0 {
		t.Fatalf("удаленный чат воскрес после хода: %+v", list)
	}
}

// Удаленный чат — в корзине неделю: вернуть можно, потом он забывается.
func TestChat_TrashRestoresAndForgets(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	_ = d.SetSettings(desktopconfig.Chat{URL: "http://127.0.0.1:11434/v1", Model: "x"}, "")
	c, err := d.Ask(askReq{Question: "сроки поставки"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	trash, err := d.Trash()
	if err != nil || len(trash) != 1 || trash[0].ID != c.ID || trash[0].Title != "сроки поставки" {
		t.Fatalf("корзина = %+v, %v", trash, err)
	}
	if err := d.Restore(c.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.List(); len(list) != 1 || len(list) == 1 && list[0].ID != c.ID {
		t.Fatalf("после «Вернуть» список = %+v", list)
	}
	if trash, _ := d.Trash(); len(trash) != 0 {
		t.Fatalf("вернутый чат остался в корзине: %+v", trash)
	}
	if err := d.Restore(c.ID); err == nil {
		t.Fatal("вернули чат, которого в корзине нет")
	}
	if err := d.Restore("../../etc"); err == nil {
		t.Fatal("путь вместо id принят")
	}

	if err := d.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	start := d.now()
	d.now = func() time.Time { return start.Add(trashKeep + time.Hour) }
	if trash, _ := d.Trash(); len(trash) != 0 {
		t.Fatalf("через неделю корзина = %+v", trash)
	}
	if err := d.Restore(c.ID); err == nil {
		t.Fatal("чат старше недели вернулся")
	}
}

// Название без невидимых знаков (ревью безопасности #662): разворот текста и
// нулевая ширина подделывают вид названия в списке.
func TestTitle_DropsInvisibleFormatChars(t *testing.T) {
	if got := title("Пос‮тав​ки⁦"); got != "Поставки" {
		t.Fatalf("title = %q", got)
	}
}
