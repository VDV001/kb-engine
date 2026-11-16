package userauth

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/mail"
)

// DevicesKey is where phones' tokens live (#622): отдельный реестр, чтобы
// токен телефона не открывал базу и финансы — сервер пускает его только в ящик.
const DevicesKey = "auth/devices.json"

// MaxDevices bounds phones per person: реестр телефонов общий, и один человек
// не должен раздувать его для всех (ревью #630).
const MaxDevices = 10

// Device is a paired phone.
type Device struct {
	Device string    `json:"device"`
	At     time.Time `json:"at"`
}

// AddDevice registers a new phone of id and returns its name and token. Токен
// отдается один раз — окну, которое показывает его в QR; на сервере только хэш.
func AddDevice(ctx context.Context, s blob.Store, id domain.UserID) (device, token string, err error) {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("userauth: device: %w", err)
	}
	device = "p-" + hex.EncodeToString(raw)
	if token, err = NewToken(); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(token))
	at := time.Now().UTC().Truncate(time.Second)
	err = update(ctx, s, DevicesKey, func(r *registryDTO) error {
		if n := len(slices.DeleteFunc(slices.Clone(r.Users), func(u userDTO) bool { return u.ID != id.String() })); n >= MaxDevices {
			return fmt.Errorf("%w: %d of %d", domain.ErrMailTooManyPhones, n, MaxDevices)
		}
		r.Users = append(r.Users, userDTO{ID: id.String(), Device: device, TokenSHA256: hex.EncodeToString(sum[:]), At: &at})
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return device, token, nil
}

// Devices lists the phones of id with their issue time.
func Devices(ctx context.Context, s blob.Store, id domain.UserID) ([]Device, error) {
	out := []Device{}
	err := read(ctx, s, DevicesKey, func(r registryDTO) {
		for _, u := range r.Users {
			if u.ID == id.String() {
				d := Device{Device: u.Device}
				if u.At != nil {
					d.At = *u.At
				}
				out = append(out, d)
			}
		}
	})
	return out, err
}

// RevokeDevice removes phone device of id; чужой телефон — ErrNoSuchUser.
func RevokeDevice(ctx context.Context, s blob.Store, id domain.UserID, device string) error {
	err := update(ctx, s, DevicesKey, func(r *registryDTO) error {
		n := len(r.Users)
		r.Users = slices.DeleteFunc(r.Users, func(u userDTO) bool { return u.ID == id.String() && u.Device == device })
		if len(r.Users) == n {
			return fmt.Errorf("%w: %s has no phone %s", domain.ErrNoSuchUser, id, device)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Подписку и вход стираем следом; не вышло — не беда: Pushes и List
	// сверяют с DevicesKey.
	_ = clearPush(ctx, s, id, device)
	_ = clearSeen(ctx, s, id, func(d string) bool { return d == device })
	return nil
}

// read reads the registry at key; нет объекта — пустой реестр.
func read(ctx context.Context, s blob.Store, key string, use func(registryDTO)) error {
	var r registryDTO
	o, err := s.Get(ctx, key)
	switch {
	case errors.Is(err, blob.ErrNotFound):
	case err != nil:
		return fmt.Errorf("userauth: read %s: %w", key, err)
	default:
		if err := json.Unmarshal(o.Data, &r); err != nil {
			return fmt.Errorf("userauth: registry %s: %w", key, err)
		}
	}
	use(r)
	return nil
}

// Phones is the phones registry as the mailbox port.
type Phones struct{ Store blob.Store }

// Add issues a phone token of id.
func (p Phones) Add(ctx context.Context, id domain.UserID) (string, string, error) {
	return AddDevice(ctx, p.Store, id)
}

// List lists the phones of id; входы к ним добавляет mail.Service из Visits.
func (p Phones) List(ctx context.Context, id domain.UserID) ([]mail.Phone, error) {
	ds, err := Devices(ctx, p.Store, id)
	if err != nil {
		return []mail.Phone{}, err
	}
	out := make([]mail.Phone, 0, len(ds))
	for _, d := range ds {
		out = append(out, mail.Phone{Device: d.Device, At: d.At})
	}
	return out, nil
}

// Revoke unpairs phone device of id.
func (p Phones) Revoke(ctx context.Context, id domain.UserID, device string) error {
	return RevokeDevice(ctx, p.Store, id, device)
}

// PushKey is where phones' push subscriptions live (#622). Отдельно от
// DevicesKey: реестр токенов читается строго (DisallowUnknownFields), и новое
// поле в нем заперло бы все телефоны при откате сервера; к тому же запись
// подписки не может испортить токены.
const PushKey = "auth/push.json"

type pushRegistryDTO struct {
	Subs []pushDTO `json:"subs"`
}

type pushDTO struct {
	ID       string `json:"id"`
	Device   string `json:"device"`
	Endpoint string `json:"endpoint"`
	P256dh   []byte `json:"p256dh"`
	Auth     []byte `json:"auth"`
}

// SetPush stores the push subscription of phone device of id; чужой или
// отвязанный телефон — ErrNoSuchUser.
func SetPush(ctx context.Context, s blob.Store, id domain.UserID, device string, sub domain.PushSubscription) error {
	if err := ownPhone(ctx, s, id, device); err != nil {
		return err
	}
	return update(ctx, s, PushKey, func(r *pushRegistryDTO) error {
		// Один адрес — одна подписка: телефон, перепривязанный новым токеном
		// (или к другому человеку), не получает уведомления дважды (ревью #643).
		// Чужую запись с тем же адресом — только при тех же ключах: это тот же
		// браузер; иначе узнавший адрес перехватил бы чужой телефон (ревью безопасности #643).
		r.Subs = slices.DeleteFunc(r.Subs, func(p pushDTO) bool {
			return (p.ID == id.String() && p.Device == device) ||
				(p.Endpoint == sub.Endpoint() && bytes.Equal(p.P256dh, sub.P256dh()) && bytes.Equal(p.Auth, sub.Auth()))
		})
		r.Subs = append(r.Subs, pushDTO{ID: id.String(), Device: device, Endpoint: sub.Endpoint(), P256dh: sub.P256dh(), Auth: sub.Auth()})
		return nil
	})
}

// ClearPush removes the subscription of phone device of id; подписки нет — не ошибка.
func ClearPush(ctx context.Context, s blob.Store, id domain.UserID, device string) error {
	if err := ownPhone(ctx, s, id, device); err != nil {
		return err
	}
	return clearPush(ctx, s, id, device)
}

func clearPush(ctx context.Context, s blob.Store, id domain.UserID, device string) error {
	return update(ctx, s, PushKey, func(r *pushRegistryDTO) error {
		r.Subs = slices.DeleteFunc(r.Subs, func(p pushDTO) bool { return p.ID == id.String() && p.Device == device })
		return nil
	})
}

// ownPhone checks that device is a live phone of id.
func ownPhone(ctx context.Context, s blob.Store, id domain.UserID, device string) error {
	ds, err := Devices(ctx, s, id)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(ds, func(d Device) bool { return d.Device == device }) {
		return fmt.Errorf("%w: %s has no phone %s", domain.ErrNoSuchUser, id, device)
	}
	return nil
}

// Pushes lists the push subscriptions of id's live phones: подписка телефона,
// которого уже нет в DevicesKey, не отдается, даже если ее не успели стереть.
func Pushes(ctx context.Context, s blob.Store, id domain.UserID) ([]mail.PhonePush, error) {
	ds, err := Devices(ctx, s, id)
	if err != nil {
		return nil, err
	}
	var r pushRegistryDTO
	o, err := s.Get(ctx, PushKey)
	switch {
	case errors.Is(err, blob.ErrNotFound):
	case err != nil:
		return nil, fmt.Errorf("userauth: read %s: %w", PushKey, err)
	default:
		if err := json.Unmarshal(o.Data, &r); err != nil {
			return nil, fmt.Errorf("userauth: registry %s: %w", PushKey, err)
		}
	}
	out := []mail.PhonePush{}
	for _, p := range r.Subs {
		if p.ID != id.String() || !slices.ContainsFunc(ds, func(d Device) bool { return d.Device == p.Device }) {
			continue
		}
		sub, err := domain.NewPushSubscription(p.Endpoint, p.P256dh, p.Auth)
		if err != nil {
			continue // правила ужесточили после записи: такой адрес больше не наш
		}
		out = append(out, mail.PhonePush{Device: p.Device, Sub: sub})
	}
	return out, nil
}

// SetPush is SetPush as the mailbox port.
func (p Phones) SetPush(ctx context.Context, id domain.UserID, device string, sub domain.PushSubscription) error {
	return SetPush(ctx, p.Store, id, device, sub)
}

// ClearPush is ClearPush as the mailbox port.
func (p Phones) ClearPush(ctx context.Context, id domain.UserID, device string) error {
	return ClearPush(ctx, p.Store, id, device)
}

// Pushes is Pushes as the mailbox port.
func (p Phones) Pushes(ctx context.Context, id domain.UserID) ([]mail.PhonePush, error) {
	return Pushes(ctx, p.Store, id)
}

// DropPush removes the subscription with this endpoint, whoever holds it:
// сервис push сказал, что адреса больше нет.
func DropPush(ctx context.Context, s blob.Store, endpoint string) error {
	return update(ctx, s, PushKey, func(r *pushRegistryDTO) error {
		r.Subs = slices.DeleteFunc(r.Subs, func(p pushDTO) bool { return p.Endpoint == endpoint })
		return nil
	})
}

// DropPush is DropPush as the mailbox port.
func (p Phones) DropPush(ctx context.Context, endpoint string) error {
	return DropPush(ctx, p.Store, endpoint)
}

// SeenKey is where the visits of id's phones live (#622): файл на человека —
// телефоны разных людей не спорят за один объект (ревью #652); отдельно от
// DevicesKey по той же причине, что PushKey, — реестр токенов читается строго.
func SeenKey(id domain.UserID) string { return "auth/seen/" + id.String() + ".json" }

// Visits writes phones' visits, не чаще VisitEvery на телефон в этом процессе:
// телефон опрашивает ящик раз в 10 с, а запись — три обращения к бакету.
//
// ponytail: кэш — в памяти процесса: у каждого экземпляра сервера свой, и при
// N экземплярах телефон пишется до N раз за VisitEvery; карта не чистится —
// потолок MaxDevices на человека, путь — срок записи в карте или кэш в бакете.
type Visits struct {
	Store blob.Store

	mu    sync.Mutex
	last  map[string]seenDTO // id|device → записанное или пишущееся сейчас
	pause time.Time          // после сбоя записи до этого момента не пишем
}

type seenFileDTO struct {
	Phones []seenDTO `json:"phones"`
}

type seenDTO struct {
	Device string    `json:"device"`
	Name   string    `json:"name,omitempty"`  // по браузеру
	Given  string    `json:"given,omitempty"` // дал человек в окне: сильнее Name
	At     time.Time `json:"at"`              // ноль — переименован, но еще не входил
}

// VisitEvery — как часто вход телефона переписывается; первый вход процесса и
// новое, более точное имя пишутся сразу.
const VisitEvery = 5 * time.Minute

// VisitRetry is the pause after a failed write: сломанный бакет не получает
// запрос и строку журнала на каждый опрос каждого телефона (ревью #652).
const VisitRetry = 30 * time.Second

// Visit notes that phone device of id came in at at, named name.
func (v *Visits) Visit(ctx context.Context, id domain.UserID, device, name string, at time.Time) error {
	k := id.String() + "|" + device
	v.mu.Lock()
	prev, ok := v.last[k]
	if ok {
		name = domain.BetterPhoneName(prev.Name, name)
	}
	if at.Before(v.pause) || ok && prev.Name == name && at.Sub(prev.At) < VisitEvery {
		v.mu.Unlock()
		return nil
	}
	if v.last == nil {
		v.last = map[string]seenDTO{}
	}
	// Занято до записи: опрос того же телефона, пришедший, пока пишется этот,
	// не пишет второй раз (ревью #652).
	mine := seenDTO{Device: device, Name: name, At: at}
	v.last[k] = mine
	v.mu.Unlock()

	wrote, err := v.write(ctx, id, seenDTO{Device: device, Name: name, At: at.UTC()})
	v.mu.Lock()
	defer v.mu.Unlock()
	if err != nil {
		// Откатываем только свою отметку: соседний опрос с другим именем мог
		// записаться, пока этот ждал, и его итог новее (ревью #652).
		switch {
		case v.last[k] != mine:
		case ok:
			v.last[k] = prev
		default:
			delete(v.last, k)
		}
		// Отмена или срок запроса телефона — не сбой бакета: пауза всем ни к чему.
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			v.pause = at.Add(VisitRetry)
		}
		return err
	}
	v.last[k] = wrote
	return nil
}

// write records entry unless its phone is gone. Имя ранжируется по записанному
// (у нового процесса кэша нет), входы отвязанных телефонов выбрасываются.
func (v *Visits) write(ctx context.Context, id domain.UserID, entry seenDTO) (seenDTO, error) {
	ds, err := Devices(ctx, v.Store, id)
	if err != nil {
		return seenDTO{}, err
	}
	live := func(device string) bool {
		return slices.ContainsFunc(ds, func(d Device) bool { return d.Device == device })
	}
	if !live(entry.Device) {
		return entry, nil // отвязан, пока шел опрос: не дописываем обратно
	}
	name := entry.Name
	err = updateSeen(ctx, v.Store, id, func(f *seenFileDTO) error {
		entry.Name, entry.Given = name, ""
		for _, e := range f.Phones {
			if e.Device == entry.Device {
				entry.Name, entry.Given = domain.BetterPhoneName(e.Name, name), e.Given
			}
		}
		f.Phones = slices.DeleteFunc(f.Phones, func(e seenDTO) bool { return e.Device == entry.Device || !live(e.Device) })
		f.Phones = append(f.Phones, entry)
		return nil
	})
	return entry, err
}

// Rename sets the name the person gave phone device of id.
// Имя живет рядом с именем браузера, а не вместо него: входы пишут свое поле
// и кэш Visits сравнивает имена браузера, а не человека.
func (v *Visits) Rename(ctx context.Context, id domain.UserID, device, name string) error {
	if err := ownPhone(ctx, v.Store, id, device); err != nil {
		return err
	}
	return updateSeen(ctx, v.Store, id, func(f *seenFileDTO) error {
		for i := range f.Phones {
			if f.Phones[i].Device == device {
				f.Phones[i].Given = name
				return nil
			}
		}
		f.Phones = append(f.Phones, seenDTO{Device: device, Given: name})
		return nil
	})
}

// Seen returns the last visits of id's phones by device.
func (v *Visits) Seen(ctx context.Context, id domain.UserID) (map[string]mail.Visit, error) {
	f, _, err := readSeen(ctx, v.Store, id)
	if err != nil {
		return nil, err
	}
	out := make(map[string]mail.Visit, len(f.Phones))
	for _, e := range f.Phones {
		out[e.Device] = mail.Visit{Name: cmp.Or(e.Given, e.Name), At: e.At}
	}
	return out, nil
}

// readSeen reads the visits file of id. Испорченный файл читается пустым:
// входы — подсказка окну, и следующий вход перепишет его, а не будет падать
// на каждом опросе (ревью #652).
func readSeen(ctx context.Context, s blob.Store, id domain.UserID) (seenFileDTO, string, error) {
	var f seenFileDTO
	o, err := s.Get(ctx, SeenKey(id))
	switch {
	case errors.Is(err, blob.ErrNotFound):
	case err != nil:
		return f, "", fmt.Errorf("userauth: read %s: %w", SeenKey(id), err)
	default:
		if json.Unmarshal(o.Data, &f) != nil {
			f = seenFileDTO{}
		}
	}
	return f, o.ETag, nil
}

// updateSeen is update for the visits file of id, but a corrupt file reads as
// empty (see readSeen); errUnchanged из change — писать нечего, файл не заводится зря.
func updateSeen(ctx context.Context, s blob.Store, id domain.UserID, change func(*seenFileDTO) error) error {
	return updateKey(ctx, s, SeenKey(id), true, change)
}

// clearSeen forgets the visits of id's phones that match.
func clearSeen(ctx context.Context, s blob.Store, id domain.UserID, match func(device string) bool) error {
	return updateSeen(ctx, s, id, func(f *seenFileDTO) error {
		n := len(f.Phones)
		if f.Phones = slices.DeleteFunc(f.Phones, func(e seenDTO) bool { return match(e.Device) }); len(f.Phones) == n {
			return errUnchanged
		}
		return nil
	})
}
