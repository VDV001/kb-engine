package mail_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mail"
)

type fakeVisits struct {
	got  []string
	seen map[string]mail.Visit
	err  error // сбой Seen
}

func (f *fakeVisits) Visit(_ context.Context, _ domain.UserID, device, name string, at time.Time) error {
	f.got = append(f.got, device+"|"+name+"|"+at.Format(time.RFC3339))
	return nil
}

func (f *fakeVisits) Seen(context.Context, domain.UserID) (map[string]mail.Visit, error) {
	return f.seen, f.err
}

// fakePhones lists fixed phones.
type fakePhones struct{ list []mail.Phone }

func (f fakePhones) Add(context.Context, domain.UserID) (string, string, error) { return "", "", nil }
func (f fakePhones) List(context.Context, domain.UserID) ([]mail.Phone, error) {
	return slices.Clone(f.list), nil
}
func (f fakePhones) Revoke(context.Context, domain.UserID, string) error { return nil }

// Вход телефона отмечается его часами сервера; компьютер не отмечается (#622).
func TestVisit(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 6, 0, 0, time.UTC)
	v := &fakeVisits{}
	svc := mail.Service{Visits: v, Now: func() time.Time { return now }}
	if err := svc.Visit(context.Background(), who, domain.MailDesk, "", "Mac"); err != nil || len(v.got) != 0 {
		t.Fatalf("desk visit: %v, %v", err, v.got)
	}
	if err := svc.Visit(context.Background(), who, domain.MailPhone, "p-1", "iPhone"); err != nil || len(v.got) != 1 || v.got[0] != "p-1|iPhone|2026-11-16T12:06:00Z" {
		t.Fatalf("phone visit: %v, %v", err, v.got)
	}
	if err := (mail.Service{}).Visit(context.Background(), who, domain.MailPhone, "p-1", "iPhone"); err != nil {
		t.Fatalf("no Visits: %v", err)
	}
}

// Список телефонов несет их входы; входы не прочитались — список все равно
// отдается (иначе не отвязать), а каждый телефон помечен «вход неизвестен»:
// окно не будет молча ждать входа, которого не увидит (ревью #652).
func TestListPhonesVisits(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 16, 12, 6, 0, 0, time.UTC)
	phones := fakePhones{list: []mail.Phone{{Device: "p-1", At: at}, {Device: "p-2", At: at}}}
	v := &fakeVisits{seen: map[string]mail.Visit{"p-1": {Name: "iPhone", At: at.Add(time.Minute)}}}
	var logged []string
	svc := mail.Service{Phones: phones, Visits: v, Logf: func(f string, a ...any) { logged = append(logged, f) }}

	list, err := svc.ListPhones(ctx, who, domain.MailDesk)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %+v, %v", list, err)
	}
	if list[0].Name != "iPhone" || !list[0].Seen.Equal(at.Add(time.Minute)) || list[0].SeenUnknown {
		t.Errorf("visited phone %+v", list[0])
	}
	if list[1].Name != "" || !list[1].Seen.IsZero() || list[1].SeenUnknown {
		t.Errorf("phone that never came in %+v", list[1])
	}

	v.err = errors.New("bucket down")
	list, err = svc.ListPhones(ctx, who, domain.MailDesk)
	if err != nil || len(list) != 2 || !list[0].SeenUnknown || !list[1].SeenUnknown {
		t.Fatalf("visits unreadable: %+v, %v", list, err)
	}
	if len(logged) != 1 {
		t.Errorf("visits failure logged %d times, want 1", len(logged))
	}
	if list, err := (mail.Service{Phones: phones}).ListPhones(ctx, who, domain.MailDesk); err != nil || len(list) != 2 || list[0].SeenUnknown {
		t.Fatalf("no Visits: %+v, %v", list, err)
	}
}
