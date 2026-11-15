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

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/domain"
)

// DevicesKey is where phones' tokens live (#622): отдельный реестр, чтобы
// токен телефона не открывал базу и финансы — сервер пускает его только в ящик.
const DevicesKey = "auth/devices.json"

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
	err = update(ctx, s, DevicesKey, func(r *registryDTO) error {
		r.Users = append(r.Users, userDTO{ID: id.String(), Device: device, TokenSHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return device, token, nil
}

// Devices lists the phones of id.
func Devices(ctx context.Context, s blob.Store, id domain.UserID) ([]string, error) {
	var out []string
	err := read(ctx, s, DevicesKey, func(r registryDTO) {
		for _, u := range r.Users {
			if u.ID == id.String() {
				out = append(out, u.Device)
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
func (p Phones) List(ctx context.Context, id domain.UserID) ([]string, error) {
	return Devices(ctx, p.Store, id)
}

// Revoke unpairs phone device of id.
func (p Phones) Revoke(ctx context.Context, id domain.UserID, device string) error {
	return RevokeDevice(ctx, p.Store, id, device)
}
