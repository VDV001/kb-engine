package mailbox_test

import (
	"context"
	"strings"
	"sync"
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
	all, err := st.Since(ctx, id, 0)
	if err != nil || len(all) != 3 || string(all[2].Sealed()) != "x3" || all[1].Chat() != "b" || all[1].From() != domain.MailDesk {
		t.Fatalf("since 0: %v, %v", all, err)
	}
	if after, _ := st.Since(ctx, id, 2); len(after) != 1 || after[0].Seq() != 3 {
		t.Errorf("since 2: %v", after)
	}
	other, _ := domain.NewUserID("other")
	if none, err := st.Since(ctx, other, 0); err != nil || len(none) != 0 {
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
			if _, err := st.Append(ctx, id, msg(t, "a", domain.MailPhone, "x")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	all, _ := st.Since(ctx, id, 0)
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
	for range 5 {
		if _, err := st.Append(ctx, id, msg(t, "a", domain.MailPhone, "x")); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := st.Since(ctx, id, 0)
	if len(all) != 3 || all[0].Seq() != 3 || all[2].Seq() != 5 {
		t.Errorf("kept %d, from %d", len(all), all[0].Seq())
	}
}
