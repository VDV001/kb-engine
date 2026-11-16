// Package mail is the phone chat mailbox (#622): телефон и компьютер дописывают
// шифротекст в журнал человека, компьютер привязывает и отвязывает телефоны.
package mail

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Page is a part of the log after a number.
type Page struct {
	Messages []domain.MailMessage
	First    int64 // первый номер, что еще в журнале
	More     bool  // есть еще после последнего
}

// Phone is a paired phone.
type Phone struct {
	Device string
	At     time.Time
}

// Box is the log of sealed messages.
type Box interface {
	Append(ctx context.Context, id domain.UserID, m domain.MailMessage) (domain.MailMessage, error)
	Since(ctx context.Context, id domain.UserID, after int64, limit int) (Page, error)
}

// Phones keeps the phones' tokens.
type Phones interface {
	Add(ctx context.Context, id domain.UserID) (device, token string, err error)
	List(ctx context.Context, id domain.UserID) ([]Phone, error)
	Revoke(ctx context.Context, id domain.UserID, device string) error
}

// PhonePush is a phone's push subscription.
type PhonePush struct {
	Device string
	Sub    domain.PushSubscription
}

// Subscriptions keeps the phones' push subscriptions.
type Subscriptions interface {
	SetPush(ctx context.Context, id domain.UserID, device string, sub domain.PushSubscription) error
	ClearPush(ctx context.Context, id domain.UserID, device string) error
	Pushes(ctx context.Context, id domain.UserID) ([]PhonePush, error)
}

// Pusher sends one push message; domain.ErrPushGone — подписки больше нет.
type Pusher interface {
	Send(ctx context.Context, sub domain.PushSubscription, payload []byte) error
}

// NotifyAnswer is the push payload: вид события, без текста. Подпись рисует
// телефон, сервер переписку не видит и в уведомление ее не кладет.
const NotifyAnswer = `{"kind":"answer"}`

// Service is the mailbox of the server.
type Service struct {
	Box    Box
	Phones Phones
	Subs   Subscriptions
	Push   Pusher
	Now    func() time.Time
	Logf   func(string, ...any)
}

// Post writes a message of side into chat.
func (s Service) Post(ctx context.Context, id domain.UserID, side domain.MailSide, chat string, sealed []byte) (domain.MailMessage, error) {
	m, err := domain.NewMailMessage(chat, side, sealed, s.Now())
	if err != nil {
		return domain.MailMessage{}, err
	}
	return s.Box.Append(ctx, id, m)
}

// Since returns up to limit messages after number after; 0 — страница по умолчанию.
func (s Service) Since(ctx context.Context, id domain.UserID, after int64, limit int) (Page, error) {
	return s.Box.Since(ctx, id, after, limit)
}

// AddPhone issues a phone token; только компьютер.
func (s Service) AddPhone(ctx context.Context, id domain.UserID, side domain.MailSide) (device, token string, err error) {
	if side != domain.MailDesk {
		return "", "", domain.ErrMailDeskOnly
	}
	return s.Phones.Add(ctx, id)
}

// ListPhones lists the phones; только компьютер.
func (s Service) ListPhones(ctx context.Context, id domain.UserID, side domain.MailSide) ([]Phone, error) {
	if side != domain.MailDesk {
		return nil, domain.ErrMailDeskOnly
	}
	return s.Phones.List(ctx, id)
}

// RevokePhone unpairs device; только компьютер.
func (s Service) RevokePhone(ctx context.Context, id domain.UserID, side domain.MailSide, device string) error {
	if side != domain.MailDesk {
		return domain.ErrMailDeskOnly
	}
	return s.Phones.Revoke(ctx, id, device)
}

// SubscribePush stores the push subscription of the calling phone.
func (s Service) SubscribePush(ctx context.Context, id domain.UserID, side domain.MailSide, device, endpoint string, p256dh, auth []byte) error {
	if side != domain.MailPhone {
		return domain.ErrMailPhoneOnly
	}
	sub, err := domain.NewPushSubscription(endpoint, p256dh, auth)
	if err != nil {
		return err
	}
	return s.Subs.SetPush(ctx, id, device, sub)
}

// UnsubscribePush removes the calling phone's subscription.
func (s Service) UnsubscribePush(ctx context.Context, id domain.UserID, side domain.MailSide, device string) error {
	if side != domain.MailPhone {
		return domain.ErrMailPhoneOnly
	}
	return s.Subs.ClearPush(ctx, id, device)
}

// Notify tells the phones the desk answered and returns how many got it.
// Рассылка идет в запросе окна, а не после ответа: в Serverless Container
// горутина после ответа замерзает. Срок — ctx вызывающего.
func (s Service) Notify(ctx context.Context, id domain.UserID, side domain.MailSide) (int, error) {
	if side != domain.MailDesk {
		return 0, domain.ErrMailDeskOnly
	}
	pushes, err := s.Subs.Pushes(ctx, id)
	if err != nil {
		return 0, err
	}
	errs := make([]error, len(pushes))
	var wg sync.WaitGroup
	for i, p := range pushes {
		wg.Go(func() { errs[i] = s.Push.Send(ctx, p.Sub, []byte(NotifyAnswer)) })
	}
	wg.Wait()
	sent := 0
	for i, err := range errs {
		switch {
		case err == nil:
			sent++
		case errors.Is(err, domain.ErrPushGone):
			if err := s.Subs.ClearPush(ctx, id, pushes[i].Device); err != nil {
				s.logf("mail: push: clear %s: %v", pushes[i].Device, err)
			}
		default:
			s.logf("mail: push %s: %v", pushes[i].Device, err)
		}
	}
	return sent, nil
}

func (s Service) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}
