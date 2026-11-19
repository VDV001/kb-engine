package userauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		if !scope.AllowsWrite() { // read и append хранятся, write - отсутствие записи
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
	limited    map[domain.UserID]domain.Scope
	bad        bool
}

// NewScopes reads the scopes lazily, on the first Scope. now nil — time.Now.
func NewScopes(ctx context.Context, s blob.Store, now func() time.Time) *Scopes {
	if now == nil {
		now = time.Now
	}
	return &Scopes{ctx: ctx, store: s, now: now}
}

// Scope returns the scope of id and whether it can be trusted. Нет доверия (файл не
// прочитать, битый, устарел) - область read и false: чтение остается, остального нет.
// Человек без записи в файле пишет, как до #701.
func (s *Scopes) Scope(id domain.UserID) (domain.Scope, bool) {
	limited, ok := s.snapshot()
	if !ok {
		return domain.ScopeRead, false
	}
	if sc, found := limited[id]; found {
		return sc, true
	}
	return domain.ScopeWrite, true
}

// snapshot returns the limited people and whether the list can be trusted.
func (s *Scopes) snapshot() (map[domain.UserID]domain.Scope, bool) {
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
		s.etag, s.limited, s.bad, s.lastOK = "", nil, false, now
	case err != nil:
		// lastOK не двигается: сбой длиннее MaxStale закроет запись всем.
	default:
		set, derr := decodeScopes(o.Data)
		if derr != nil {
			s.etag, s.limited, s.bad, s.lastOK = "", nil, true, now
			if s.Logf != nil { // раз в RefreshEvery, пока файл не починят
				s.Logf("%s: %v: запись закрыта всем, чтение открыто", ScopesKey, derr)
			}
		} else {
			s.etag, s.limited, s.bad, s.lastOK = o.ETag, set, false, now
		}
	}
	return s.fresh(now)
}

// fresh returns the set while it is younger than MaxStale and not corrupt.
func (s *Scopes) fresh(now time.Time) (map[domain.UserID]domain.Scope, bool) {
	if s.bad || s.lastOK.IsZero() || now.Sub(s.lastOK) > MaxStale {
		return nil, false
	}
	return s.limited, true
}

func decodeScopes(data []byte) (map[domain.UserID]domain.Scope, error) {
	// encoding/json берет последний из повторных ключей, принимает null и хвост после
	// объекта: файл, правленный руками, мог бы молча открыть запись (#704, N4).
	if err := rejectAmbiguousJSON(data); err != nil {
		return nil, err
	}
	var dto scopesDTO
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // опечатка в имени поля при ручной правке - битый файл, а не «человек пишет»
	if err := dec.Decode(&dto); err != nil {
		return nil, err
	}
	set := make(map[domain.UserID]domain.Scope, len(dto.Scopes))
	for _, e := range dto.Scopes {
		id, err := domain.NewUserID(e.ID)
		if err != nil {
			return nil, err
		}
		scope, err := domain.NewScope(e.Scope)
		if err != nil {
			return nil, err
		}
		if _, dup := set[id]; dup {
			return nil, fmt.Errorf("%w: %q listed twice", errAmbiguousScopes, e.ID)
		}
		set[id] = scope
	}
	for id, scope := range set {
		if scope.AllowsWrite() { // «write» в файле - то же, что отсутствие записи
			delete(set, id)
		}
	}
	return set, nil
}

var errAmbiguousScopes = errors.New("ambiguous scopes file")

// rejectAmbiguousJSON refuses what encoding/json would read in more than one way: не
// объект, повторный ключ на любой глубине, данные после объекта.
func rejectAmbiguousJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var tr keyTracker
	for first := true; ; first = false {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); first && (!ok || d != '{') {
			return fmt.Errorf("%w: the file must be a JSON object", errAmbiguousScopes)
		}
		if isKey, err := tr.key(tok); err != nil {
			return err
		} else if isKey {
			continue
		}
		if tr.value(tok) { // объект верхнего уровня закрыт: дальше ничего быть не должно
			if dec.InputOffset() < int64(len(bytes.TrimRight(data, " \t\r\n"))) {
				return fmt.Errorf("%w: data after the object", errAmbiguousScopes)
			}
			return nil
		}
	}
}

// keyTracker follows the nesting of a token stream and the keys seen in each object.
type keyTracker struct{ stack []keyFrame }

type keyFrame struct {
	keys    map[string]bool // nil у массива
	wantKey bool
}

// key consumes tok when it is an object key, and fails on a repeated one.
func (t *keyTracker) key(tok json.Token) (bool, error) {
	if len(t.stack) == 0 {
		return false, nil
	}
	top := &t.stack[len(t.stack)-1]
	k, ok := tok.(string)
	if !top.wantKey || !ok {
		return false, nil
	}
	if top.keys[k] {
		return false, fmt.Errorf("%w: key %q repeated", errAmbiguousScopes, k)
	}
	top.keys[k], top.wantKey = true, false
	return true, nil
}

// value consumes tok as (part of) a value and reports whether the top-level value ended.
func (t *keyTracker) value(tok json.Token) bool {
	d, isDelim := tok.(json.Delim)
	switch {
	case isDelim && d == '{':
		t.stack = append(t.stack, keyFrame{keys: map[string]bool{}, wantKey: true})
		return false
	case isDelim && d == '[':
		t.stack = append(t.stack, keyFrame{})
		return false
	case isDelim: // закрытие: } или ]
		t.stack = t.stack[:len(t.stack)-1]
		if len(t.stack) == 0 {
			return true
		}
	}
	if top := &t.stack[len(t.stack)-1]; top.keys != nil { // значение прочитано: дальше ключ
		top.wantKey = true
	}
	return false
}
