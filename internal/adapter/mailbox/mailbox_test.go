package mailbox_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
	"github.com/daniil/kb-engine/internal/adapter/mailbox"
	"github.com/daniil/kb-engine/internal/domain"
)

func setup(t *testing.T) (mailbox.Store, blob.Store, domain.UserID) {
	t.Helper()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.NewUserID("owner")
	if err != nil {
		t.Fatal(err)
	}
	return mailbox.Store{Blob: s}, s, id
}

func msg(t *testing.T, chat string, from domain.MailSide, text string) domain.MailMessage {
	t.Helper()
	m, err := domain.NewMailMessage(chat, from, []byte(text), time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Журнал только дописывается: номера идут подряд, «новое после N» — по всем чатам.
func TestAppendSince(t *testing.T) {
	ctx := context.Background()
	st, raw, id := setup(t)
	for i, m := range []domain.MailMessage{msg(t, "a", domain.MailPhone, "x1"), msg(t, "b", domain.MailDesk, "x2"), msg(t, "a", domain.MailDesk, "x3")} {
		got, err := st.Append(ctx, id, m)
		if err != nil || got.Seq() != int64(i+1) {
			t.Fatalf("append %d: seq %d, %v", i, got.Seq(), err)
		}
	}
	p, err := st.Since(ctx, id, 0, 0)
	all := p.Messages
	if err != nil || len(all) != 3 || string(all[2].Sealed()) != "x3" || all[1].Chat() != "b" || all[1].From() != domain.MailDesk {
		t.Fatalf("since 0: %v, %v", all, err)
	}
	if p, _ := st.Since(ctx, id, 2, 0); len(p.Messages) != 1 || p.Messages[0].Seq() != 3 {
		t.Errorf("since 2: %v", p.Messages)
	}
	other, _ := domain.NewUserID("other")
	if none, err := st.Since(ctx, other, 0, 0); err != nil || len(none.Messages) != 0 {
		t.Errorf("someone else's box: %v, %v", none, err)
	}
	o, err := raw.Get(ctx, "users/owner/mail/log.jsonl")
	if err != nil || strings.Contains(string(o.Data), "x1") {
		t.Errorf("stored as is, not as base64 of the ciphertext: %s", o.Data)
	}
}

// Двое пишут сразу (телефон и компьютер): ни одно сообщение не теряется, номера разные.
func TestAppendConcurrent(t *testing.T) {
	ctx := context.Background()
	st, _, id := setup(t)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if _, err := st.Append(ctx, id, msg(t, "a", domain.MailPhone, randText())); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	p, _ := st.Since(ctx, id, 0, 0)
	all := p.Messages
	seen := map[int64]bool{}
	for _, m := range all {
		seen[m.Seq()] = true
	}
	if len(all) != 6 || len(seen) != 6 {
		t.Errorf("%d messages, %d distinct numbers; want 6 and 6", len(all), len(seen))
	}
}

// Журнал не растет бесконечно: старое уходит, номера не начинаются заново.
func TestAppendKeepsLast(t *testing.T) {
	ctx := context.Background()
	st, _, id := setup(t)
	st.Keep = 3
	for i := range 5 {
		if _, err := st.Append(ctx, id, msg(t, "a", domain.MailPhone, "x"+string(rune('0'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := st.Since(ctx, id, 0, 0)
	all := p.Messages
	if len(all) != 3 || all[0].Seq() != 3 || all[2].Seq() != 5 {
		t.Errorf("kept %d, from %d", len(all), all[0].Seq())
	}
}

// Журнал держится в бюджете байт: большие сообщения вытесняют старые, а не
// заклинивают ящик пределом объекта хранилища (ревью #630).
func TestAppendKeepsByteBudget(t *testing.T) {
	ctx := context.Background()
	st, raw, id := setup(t)
	st.MaxBytes = 300 << 10
	big := strings.Repeat("я", domain.MaxMailSealed/2)
	for i := range 20 {
		if _, err := st.Append(ctx, id, msg(t, "a", domain.MailPhone, big+string(rune('a'+i)))); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	o, err := raw.Get(ctx, "users/owner/mail/log.jsonl")
	if err != nil || len(o.Data) > 300<<10 {
		t.Fatalf("log %d bytes, budget %d: %v", len(o.Data), 300<<10, err)
	}
	p, _ := st.Since(ctx, id, 0, 0)
	if len(p.Messages) == 0 || p.Messages[len(p.Messages)-1].Seq() != 20 {
		t.Errorf("the newest is lost: %d kept", len(p.Messages))
	}
}

// Повтор той же записи (сбой хранилища после записи, повтор клиента) не
// дописывает второе сообщение: шифротекст с нонсом уникален (ревью #630).
func TestAppendRetryIsNotADuplicate(t *testing.T) {
	ctx := context.Background()
	st, _, id := setup(t)
	m := msg(t, "a", domain.MailPhone, "x1")
	a, _ := st.Append(ctx, id, m)
	b, err := st.Append(ctx, id, m)
	if err != nil || a.Seq() != b.Seq() {
		t.Fatalf("retry: seq %d then %d, %v", a.Seq(), b.Seq(), err)
	}
	if p, _ := st.Since(ctx, id, 0, 0); len(p.Messages) != 1 {
		t.Errorf("%d messages after a retry", len(p.Messages))
	}
}

// Ответ ограничен; клиент видит, что есть еще и с какого номера журнал начинается.
func TestSincePage(t *testing.T) {
	ctx := context.Background()
	st, _, id := setup(t)
	st.Keep = 4
	for i := range 6 {
		if _, err := st.Append(ctx, id, msg(t, "a", domain.MailPhone, "x"+string(rune('0'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	p, err := st.Since(ctx, id, 0, 2)
	if err != nil || len(p.Messages) != 2 || !p.More || p.First != 3 || p.Messages[0].Seq() != 3 {
		t.Fatalf("page %+v, %v; want 3..4, more, first 3", p, err)
	}
	if p, _ := st.Since(ctx, id, 4, 2); len(p.Messages) != 2 || p.More {
		t.Errorf("last page %+v", p)
	}
}

var textN atomic.Int64

// randText — разный шифротекст на каждый вызов, как у настоящего AES-GCM с нонсом.
func randText() string { return "x" + strconv.FormatInt(textN.Add(1), 10) }
