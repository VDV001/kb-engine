// Package mail is the phone chat mailbox (#622): телефон и компьютер дописывают
// шифротекст в журнал человека, компьютер привязывает и отвязывает телефоны.
package mail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
	Seen   time.Time // последний вход телефона; ноль — еще не входил
	Name   string    // имя по браузеру телефона: «iPhone», «Samsung SM-S918B»
	// SeenUnknown — входы не прочитались: ноль в Seen не значит «не входил».
	SeenUnknown bool
}

// Visit is a phone's last visit.
type Visit struct {
	Name string
	At   time.Time
}

// Visits remembers when a phone last came in and what it calls itself.
type Visits interface {
	Visit(ctx context.Context, id domain.UserID, device, name string, at time.Time) error
	// Seen returns the last visits of id's phones by device.
	Seen(ctx context.Context, id domain.UserID) (map[string]Visit, error)
	// Rename sets the name the person gave the phone; чужой телефон — ErrNoSuchUser.
	Rename(ctx context.Context, id domain.UserID, device, name string) error
}

// RenamePhone names a phone as the person wants; только компьютер.
// Без Visits — errors.ErrUnsupported: имени негде жить.
func (s Service) RenamePhone(ctx context.Context, id domain.UserID, side domain.MailSide, device, name string) error {
	if side != domain.MailDesk {
		return domain.ErrMailDeskOnly
	}
	if s.Visits == nil {
		return errors.ErrUnsupported
	}
	name, err := domain.NewPhoneName(name)
	if err != nil {
		return err
	}
	return s.Visits.Rename(ctx, id, device, name)
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
	// DropPush removes the subscription with endpoint, чья бы она ни была.
	DropPush(ctx context.Context, endpoint string) error
}

// Pusher sends one push message; domain.ErrPushGone — подписки больше нет.
type Pusher interface {
	Send(ctx context.Context, sub domain.PushSubscription, payload []byte) error
	// PublicKey is the server's VAPID key for the phone's subscribe call.
	PublicKey(ctx context.Context) (string, error)
}

// NotifyAnswer is the push payload: вид события, без текста. Подпись рисует
// телефон, сервер переписку не видит и в уведомление ее не кладет.
const NotifyAnswer = `{"kind":"answer"}`

// NotifyTimeout bounds one notify call for all phones together.
const NotifyTimeout = 5 * time.Second

// Files keeps the sealed file pieces of the mailbox (#658): голос и вложения
// телефона идут мимо журнала — журнал один объект и переписывается целиком.
type Files interface {
	Put(ctx context.Context, id domain.UserID, file string, sealed []byte) error
	// Get returns the piece; нет его или он чужой — domain.ErrNoSuchMailFile.
	Get(ctx context.Context, id domain.UserID, file string) ([]byte, error)
	// Delete removes the piece; нет его — не ошибка.
	Delete(ctx context.Context, id domain.UserID, file string) error
}

// Service is the mailbox of the server.
type Service struct {
	Box    Box
	Phones Phones
	Subs   Subscriptions
	Push   Pusher
	Visits Visits
	Files  Files // nil — файлов нет: domain.ErrMailFilesOff
	Now    func() time.Time
	Logf   func(string, ...any)
}

// PutFile keeps a sealed file piece of either side and returns its id: id
// выдает сервер — случайный, клиент его не выбирает и чужой не перезапишет.
func (s Service) PutFile(ctx context.Context, id domain.UserID, side domain.MailSide, sealed []byte) (string, error) {
	if _, err := domain.ParseMailSide(string(side)); err != nil {
		return "", err
	}
	switch {
	case len(sealed) == 0:
		return "", domain.ErrMailEmpty
	case len(sealed) > domain.MaxMailFile:
		return "", fmt.Errorf("%w: %d bytes, limit %d", domain.ErrMailTooLarge, len(sealed), domain.MaxMailFile)
	case s.Files == nil:
		return "", domain.ErrMailFilesOff
	}
	var raw [16]byte
	_, _ = rand.Read(raw[:]) // с Go 1.24 Read не возвращает ошибку
	file := hex.EncodeToString(raw[:])
	if err := s.Files.Put(ctx, id, file, sealed); err != nil {
		return "", err // id без записи — ссылка в никуда
	}
	return file, nil
}

// GetFile returns a piece of the person's mailbox.
func (s Service) GetFile(ctx context.Context, id domain.UserID, file string) ([]byte, error) {
	if err := s.fileCall(file); err != nil {
		return nil, err
	}
	return s.Files.Get(ctx, id, file)
}

// DeleteFile removes a piece: получатель забрал его.
func (s Service) DeleteFile(ctx context.Context, id domain.UserID, file string) error {
	if err := s.fileCall(file); err != nil {
		return err
	}
	return s.Files.Delete(ctx, id, file)
}

func (s Service) fileCall(file string) error {
	if err := domain.ValidateMailFileID(file); err != nil {
		return err
	}
	if s.Files == nil {
		return domain.ErrMailFilesOff
	}
	return nil
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

// ListPhones lists the phones with their visits; только компьютер. Входы не
// прочитались — список все равно отдается (иначе не отвязать), а телефоны
// помечены SeenUnknown: окно не ждет молча входа, которого не увидит.
func (s Service) ListPhones(ctx context.Context, id domain.UserID, side domain.MailSide) ([]Phone, error) {
	if side != domain.MailDesk {
		return nil, domain.ErrMailDeskOnly
	}
	list, err := s.Phones.List(ctx, id)
	if err != nil || s.Visits == nil {
		return list, err
	}
	seen, err := s.Visits.Seen(ctx, id)
	if err != nil {
		s.logf("mail: visits: %v", err)
	}
	for i := range list {
		if v, ok := seen[list[i].Device]; ok {
			list[i].Name, list[i].Seen = v.Name, v.At
		}
		list[i].SeenUnknown = err != nil
	}
	return list, nil
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
	if s.Push == nil {
		return domain.ErrPushOff
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
func (s Service) Notify(parent context.Context, id domain.UserID, side domain.MailSide) (int, error) {
	if side != domain.MailDesk {
		return 0, domain.ErrMailDeskOnly
	}
	if s.Push == nil {
		return 0, domain.ErrPushOff
	}
	ctx, cancel := context.WithTimeout(parent, NotifyTimeout)
	defer cancel()
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
			// По адресу, а не по телефону: телефон мог уже положить новый адрес
			// на место забытого. Своим сроком: общий мог истечь на медленном соседе.
			if err := s.dropPush(parent, pushes[i].Sub.Endpoint()); err != nil {
				s.logf("mail: push: drop %s: %v", pushes[i].Device, err)
			}
		default:
			s.logf("mail: push %s: %v", pushes[i].Device, err)
		}
	}
	return sent, nil
}

// dropTimeout bounds removing one gone subscription.
const dropTimeout = 5 * time.Second

func (s Service) dropPush(parent context.Context, endpoint string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), dropTimeout)
	defer cancel()
	return s.Subs.DropPush(ctx, endpoint)
}

func (s Service) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// PushKey is the server's public VAPID key; push не настроен — ErrPushOff.
func (s Service) PushKey(ctx context.Context) (string, error) {
	if s.Push == nil {
		return "", domain.ErrPushOff
	}
	return s.Push.PublicKey(ctx)
}

// Visit notes that the calling phone came in; компьютер не отмечается, без
// Visits — ничего. Сбой — ошибка: вызывающий только пишет ее в журнал.
func (s Service) Visit(ctx context.Context, id domain.UserID, side domain.MailSide, device, name string) error {
	if side != domain.MailPhone || s.Visits == nil {
		return nil
	}
	return s.Visits.Visit(ctx, id, device, name, s.Now())
}
