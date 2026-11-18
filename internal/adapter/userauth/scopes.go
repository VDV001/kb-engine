package userauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/domain"
)

// ScopesKey is where the people with a limited scope are listed (#701). Отдельный
// от реестров токенов объект: реестр читается строго, и новое поле в нем при
// откате сервера заперло бы всех людей; этот файл старый код не читает вовсе.
const ScopesKey = "auth/scopes.json"

type scopesDTO struct {
	Scopes []scopeDTO `json:"scopes"`
}

type scopeDTO struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
}

// SetScope records the scope of a person; ScopeWrite снимает запись (человек без
// записи пишет, как прежде). Повтор и снятие отсутствующей записи — не ошибка и
// не запись в хранилище.
func SetScope(ctx context.Context, s blob.Store, id domain.UserID, scope domain.Scope) error {
	return update(ctx, s, ScopesKey, func(d *scopesDTO) error {
		before := slices.Clone(d.Scopes)
		d.Scopes = slices.DeleteFunc(d.Scopes, func(e scopeDTO) bool { return e.ID == id.String() })
		if !scope.AllowsWrite() {
			d.Scopes = append(d.Scopes, scopeDTO{ID: id.String(), Scope: scope.String()})
		}
		if slices.Equal(before, d.Scopes) {
			return errUnchanged
		}
		return nil
	})
}

// Scopes answers whether a person may change data. Читает ScopesKey лениво и
// держит список RefreshEvery, как Registry, но ЗАКРЫТО, а не открыто: файл,
// который нельзя прочитать (сбой дольше MaxStale, ни разу не прочитан, битый),
// значит «пишет никто», читают все. Нет файла — ограниченных нет, все пишут.
type Scopes struct {
	// Logf, если задан, получает причину, по которой файл областей закрыл запись всем.
	Logf func(format string, args ...any)

	ctx   context.Context
	store blob.Store
	now   func() time.Time

	mu         sync.Mutex
	checked    time.Time
	lastOK     time.Time
	refreshing bool
	refreshed  chan struct{}
	etag       string
	readOnly   map[domain.UserID]struct{}
	bad        bool
}

// NewScopes reads the scopes lazily, on the first Writable. now nil — time.Now.
func NewScopes(ctx context.Context, s blob.Store, now func() time.Time) *Scopes {
	if now == nil {
		now = time.Now
	}
	return &Scopes{ctx: ctx, store: s, now: now}
}

// Scope returns the scope of id and whether it can be trusted (stub).
func (s *Scopes) Scope(id domain.UserID) (domain.Scope, bool) { return domain.ScopeWrite, true }

// Writable reports whether id's token may change data.
func (s *Scopes) Writable(id domain.UserID) bool {
	readOnly, ok := s.snapshot()
	if !ok {
		return false
	}
	_, limited := readOnly[id]
	return !limited
}

// snapshot returns the read-only set and whether it can be trusted.
func (s *Scopes) snapshot() (map[domain.UserID]struct{}, bool) {
	s.mu.Lock()
	now := s.now()
	due := s.lastOK.IsZero() || now.Sub(s.checked) >= RefreshEvery
	if s.refreshing {
		if s.lastOK.IsZero() { // холодный старт: ждем первое чтение, а не отказываем вслепую
			done := s.refreshed
			s.mu.Unlock()
			<-done
			s.mu.Lock()
		}
		defer s.mu.Unlock()
		return s.fresh(s.now())
	}
	if !due {
		defer s.mu.Unlock()
		return s.fresh(now)
	}
	s.refreshing, s.refreshed = true, make(chan struct{})
	etag := s.etag
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(s.ctx, refreshTimeout)
	o, err := s.store.GetIfChanged(ctx, ScopesKey, etag)
	cancel()

	s.mu.Lock()
	defer s.mu.Unlock()
	defer close(s.refreshed)
	s.refreshing, s.checked = false, now
	switch {
	case errors.Is(err, blob.ErrNotModified):
		s.lastOK = now
	case errors.Is(err, blob.ErrNotFound):
		s.etag, s.readOnly, s.bad, s.lastOK = "", nil, false, now
	case err != nil:
		// lastOK не двигается: сбой длиннее MaxStale закроет запись всем.
	default:
		set, derr := decodeScopes(o.Data)
		if derr != nil {
			s.etag, s.readOnly, s.bad, s.lastOK = "", nil, true, now
			if s.Logf != nil { // раз в RefreshEvery, пока файл не починят
				s.Logf("%s: %v: запись закрыта всем, чтение открыто", ScopesKey, derr)
			}
		} else {
			s.etag, s.readOnly, s.bad, s.lastOK = o.ETag, set, false, now
		}
	}
	return s.fresh(now)
}

// fresh returns the set while it is younger than MaxStale and not corrupt.
func (s *Scopes) fresh(now time.Time) (map[domain.UserID]struct{}, bool) {
	if s.bad || s.lastOK.IsZero() || now.Sub(s.lastOK) > MaxStale {
		return nil, false
	}
	return s.readOnly, true
}

func decodeScopes(data []byte) (map[domain.UserID]struct{}, error) {
	var dto scopesDTO
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // опечатка в имени поля при ручной правке - битый файл, а не «человек пишет»
	if err := dec.Decode(&dto); err != nil {
		return nil, err
	}
	set := make(map[domain.UserID]struct{}, len(dto.Scopes))
	for _, e := range dto.Scopes {
		id, err := domain.NewUserID(e.ID)
		if err != nil {
			return nil, err
		}
		scope, err := domain.NewScope(e.Scope)
		if err != nil {
			return nil, err
		}
		if !scope.AllowsWrite() {
			set[id] = struct{}{}
		}
	}
	return set, nil
}
