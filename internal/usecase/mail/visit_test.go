package mail_test

import (
	"context"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mail"
)

type fakeVisits struct{ got []string }

func (f *fakeVisits) Visit(_ context.Context, _ domain.UserID, device, name string, at time.Time) error {
	f.got = append(f.got, device+"|"+name+"|"+at.Format(time.RFC3339))
	return nil
}

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
