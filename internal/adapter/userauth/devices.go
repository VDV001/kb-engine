package userauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	return update(ctx, s, DevicesKey, func(r *registryDTO) error {
		n := len(r.Users)
		r.Users = slices.DeleteFunc(r.Users, func(u userDTO) bool { return u.ID == id.String() && u.Device == device })
		if len(r.Users) == n {
			return fmt.Errorf("%w: %s has no phone %s", domain.ErrNoSuchUser, id, device)
		}
		return nil
	})
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

// List lists the phones of id.
func (p Phones) List(ctx context.Context, id domain.UserID) ([]mail.Phone, error) {
	ds, err := Devices(ctx, p.Store, id)
	out := make([]mail.Phone, 0, len(ds))
	for _, d := range ds {
		out = append(out, mail.Phone{Device: d.Device, At: d.At})
	}
	return out, err
}

// Revoke unpairs phone device of id.
func (p Phones) Revoke(ctx context.Context, id domain.UserID, device string) error {
	return RevokeDevice(ctx, p.Store, id, device)
}

// SetPush stores the push subscription of phone device of id; чужой или
// отвязанный телефон — ErrNoSuchUser.
func SetPush(ctx context.Context, s blob.Store, id domain.UserID, device string, sub domain.PushSubscription) error {
	return errors.ErrUnsupported
}

// ClearPush removes the subscription of phone device of id.
func ClearPush(ctx context.Context, s blob.Store, id domain.UserID, device string) error {
	return errors.ErrUnsupported
}

// Pushes lists the push subscriptions of id's phones.
func Pushes(ctx context.Context, s blob.Store, id domain.UserID) ([]mail.PhonePush, error) {
	return nil, errors.ErrUnsupported
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
