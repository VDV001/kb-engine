// Package userauth keeps the people of a multi-tenant server and their tokens.
//
// Реестр — объект auth/users.json в том же хранилище, что и базы: имя
// человека и SHA-256 его токена. Самих токенов на сервере нет нигде: add
// печатает токен один раз, и украденный реестр войти не дает. Запись реестра
// условная (blob.Store.Put с ETag), поэтому два одновременных add или revoke не
// теряют друг друга.
package userauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/domain"
)

// RegistryKey is where the registry lives in the store.
const RegistryKey = "auth/users.json"

// RefreshEvery is how often the server re-reads the registry: отзыв токена
// доходит до сервера не позже чем за этот срок.
//
// ponytail: отзыв доходит до минуты. Путь апгрейда — перечитывать реестр по
// событию записи (уведомления Object Storage), а не по таймеру.
const RefreshEvery = time.Minute

// writeAttempts bounds retries of a registry write after ErrConflict.
const writeAttempts = 5

var (
	// ErrUserExists — add для имени, которое уже есть.
	ErrUserExists = errors.New("userauth: user already exists")
	// ErrNoSuchUser — revoke для имени, которого нет.
	ErrNoSuchUser = errors.New("userauth: no such user")
)

// CatalogKey is where a person's catalog lives: база каждого — под своим
// users/<id>/, с той же раскладкой, что knowledge-base на диске.
func CatalogKey(id domain.UserID) string {
	return "users/" + id.String() + "/kb/_data/catalog.json"
}

type registryDTO struct {
	Users []userDTO `json:"users"`
}

type userDTO struct {
	ID          string `json:"id"`
	TokenSHA256 string `json:"token_sha256"`
}

// Add registers id and returns its token. Токен печатается вызывающим один раз
// и больше нигде не хранится. Человеку сразу заводится пустая база, если ее
// еще нет: сервер не падает на первом входе, а повторный add после revoke
// возвращает доступ к прежней базе.
func Add(ctx context.Context, s blob.Store, id domain.UserID) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("userauth: token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw) // 43 печатных символа
	if _, err := s.Put(ctx, CatalogKey(id), []byte("{\"entries\":[]}\n"), ""); err != nil && !errors.Is(err, blob.ErrConflict) {
		return "", fmt.Errorf("userauth: empty catalog for %s: %w", id, err)
	}
	sum := sha256.Sum256([]byte(token))
	err := update(ctx, s, func(r *registryDTO) error {
		if slices.ContainsFunc(r.Users, func(u userDTO) bool { return u.ID == id.String() }) {
			return fmt.Errorf("%w: %s", ErrUserExists, id)
		}
		r.Users = append(r.Users, userDTO{ID: id.String(), TokenSHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// Revoke removes id from the registry. База человека остается в хранилище:
// отзыв закрывает вход, а не удаляет данные.
func Revoke(ctx context.Context, s blob.Store, id domain.UserID) error {
	return update(ctx, s, func(r *registryDTO) error {
		n := len(r.Users)
		r.Users = slices.DeleteFunc(r.Users, func(u userDTO) bool { return u.ID == id.String() })
		if len(r.Users) == n {
			return fmt.Errorf("%w: %s", ErrNoSuchUser, id)
		}
		return nil
	})
}

// update reads the registry, applies change and writes it back under the
// version it read, retrying when someone else wrote in between.
func update(ctx context.Context, s blob.Store, change func(*registryDTO) error) error {
	for range writeAttempts {
		var r registryDTO
		o, err := s.Get(ctx, RegistryKey)
		switch {
		case errors.Is(err, blob.ErrNotFound):
		case err != nil:
			return fmt.Errorf("userauth: read registry: %w", err)
		default:
			if err := json.Unmarshal(o.Data, &r); err != nil {
				return fmt.Errorf("userauth: registry %s: %w", RegistryKey, err)
			}
		}
		if err := change(&r); err != nil {
			return err
		}
		data, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		_, err = s.Put(ctx, RegistryKey, append(data, '\n'), o.ETag)
		if errors.Is(err, blob.ErrConflict) {
			continue
		}
		if err != nil {
			return fmt.Errorf("userauth: write registry: %w", err)
		}
		return nil
	}
	return fmt.Errorf("userauth: registry kept changing under %d attempts: %w", writeAttempts, blob.ErrConflict)
}

// refreshTimeout bounds one re-read of the registry.
const refreshTimeout = 10 * time.Second

// Registry answers whose token a request carries.
type Registry struct {
	ctx   context.Context
	store blob.Store
	now   func() time.Time

	mu      sync.Mutex
	checked time.Time
	etag    string
	users   []knownUser
}

type knownUser struct {
	id  domain.UserID
	sum []byte
}

// Option configures a Registry.
type Option func(*Registry)

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(r *Registry) { r.now = now } }

// NewRegistry reads the registry from s lazily, on the first Lookup.
func NewRegistry(ctx context.Context, s blob.Store, opts ...Option) *Registry {
	r := &Registry{ctx: ctx, store: s, now: time.Now}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Lookup returns the person whose token this is. Хэш токена сравнивается со
// всеми записями за постоянное время и без раннего выхода: время ответа не
// выдает, на какой записи остановились.
func (r *Registry) Lookup(token string) (domain.UserID, bool) {
	if token == "" {
		return domain.UserID{}, false
	}
	users := r.current()
	sum := sha256.Sum256([]byte(token))
	var found domain.UserID
	ok := false
	for _, u := range users {
		if subtle.ConstantTimeCompare(sum[:], u.sum) == 1 {
			found, ok = u.id, true
		}
	}
	return found, ok
}

// current returns the known users, re-reading the registry once RefreshEvery
// has passed. Битый реестр не пускает никого (закрыто, а не открыто); сбой
// связи с хранилищем оставляет последний прочитанный список.
//
// ponytail: замок держится на время перечитывания. Потолок — раз в минуту
// запросы ждут один условный GET. Путь апгрейда — перечитывать в фоне.
func (r *Registry) current() []knownUser {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if !r.checked.IsZero() && now.Sub(r.checked) < RefreshEvery {
		return r.users
	}
	r.checked = now
	ctx, cancel := context.WithTimeout(r.ctx, refreshTimeout)
	defer cancel()
	o, err := r.store.GetIfChanged(ctx, RegistryKey, r.etag)
	switch {
	case errors.Is(err, blob.ErrNotModified):
		return r.users
	case errors.Is(err, blob.ErrNotFound):
		r.etag, r.users = "", nil
		return nil
	case err != nil:
		return r.users
	}
	users, err := decodeUsers(o.Data)
	if err != nil {
		r.etag, r.users = "", nil
		return nil
	}
	r.etag, r.users = o.ETag, users
	return users
}

func decodeUsers(data []byte) ([]knownUser, error) {
	var dto registryDTO
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&dto); err != nil {
		return nil, err
	}
	users := make([]knownUser, 0, len(dto.Users))
	for _, u := range dto.Users {
		id, err := domain.NewUserID(u.ID)
		if err != nil {
			return nil, err
		}
		sum, err := hex.DecodeString(u.TokenSHA256)
		if err != nil || len(sum) != sha256.Size {
			return nil, fmt.Errorf("userauth: %s: bad token_sha256", u.ID)
		}
		users = append(users, knownUser{id: id, sum: sum})
	}
	return users, nil
}
