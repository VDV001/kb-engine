package userauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

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

// ErrScopesBroken is returned when the scopes file cannot be read as a list of limits: менять его нельзя.
var ErrScopesBroken = errors.New("scopes file is broken")

// checkScopes refuses a file the reader would not trust. Совет «удалить файл» опасен: нет
// файла - значит «ограниченных нет», и запись открылась бы всем.
func checkScopes(data []byte) error {
	if _, err := decodeScopes(data); err != nil {
		return fmt.Errorf("%w: %s: repair it or re-create it with the intended entries (removing the file lifts every limit): %w", ErrScopesBroken, ScopesKey, err)
	}
	return nil
}

// SetScope records the scope of a person; ScopeWrite снимает запись (человек без
// записи пишет, как прежде). Повтор и снятие отсутствующей записи — не ошибка и
// не запись в хранилище.
func SetScope(ctx context.Context, s blob.Store, id domain.UserID, scope domain.Scope) error {
	// Файл, который читатель считает битым, переписывать нельзя: update читает мягко (последний
	// ключ побеждает) и молча снял бы ограничения, которые читатель закрыл (ревью #710).
	return updateKey(ctx, s, ScopesKey, false, checkScopes, func(d *scopesDTO) error {
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
	// Один проход, одно дерево: encoding/json брал последний из повторных ключей, принимал
	// null и хвост после объекта, а файл, правленный руками, мог бы молча открыть запись
	// (#704, N4). Поля берутся из того же дерева, второго разбора нет (ревью #710).
	dec := json.NewDecoder(bytes.NewReader(data))
	doc, err := readStrict(dec)
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: the file is empty", errAmbiguousScopes)
	}
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: data after the object", errAmbiguousScopes)
	}
	top, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: the file must be a JSON object", errAmbiguousScopes)
	}
	entries, err := scopeEntries(top)
	if err != nil {
		return nil, err
	}
	set := make(map[domain.UserID]domain.Scope, len(entries))
	for _, e := range entries {
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

// Имена полей файла в той форме, в какой readStrict хранит ключи (foldKey).
var keyScopes, keyID, keyScope = foldKey("scopes"), foldKey("id"), foldKey("scope")

// scopeEntries takes the list out of the document: поле scopes и в записи id и scope,
// имена без учета регистра, как их читал encoding/json; любое другое имя - битый файл.
func scopeEntries(top map[string]any) ([]scopeDTO, error) {
	var list []any
	for k, v := range top {
		if k != keyScopes {
			return nil, fmt.Errorf("%w: unknown field %q", errAmbiguousScopes, strings.ToLower(k))
		}
		switch l := v.(type) {
		case []any:
			list = l
		case nil: // "scopes": null - пустой список, как у encoding/json
		default:
			return nil, fmt.Errorf("%w: scopes must be a list", errAmbiguousScopes)
		}
	}
	out := make([]scopeDTO, 0, len(list))
	for _, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: a scopes entry must be an object", errAmbiguousScopes)
		}
		var e scopeDTO
		for k, v := range obj {
			str, isStr := v.(string)
			switch {
			case k == keyID && isStr:
				e.ID = str
			case k == keyScope && isStr:
				e.Scope = str
			default:
				return nil, fmt.Errorf("%w: bad field %q in a scopes entry", errAmbiguousScopes, strings.ToLower(k))
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// readStrict reads one JSON value into maps, slices and scalars, and fails on a repeated
// object key at any depth. Ключи хранятся в свернутом виде (foldKey).
func readStrict(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil
	}
	switch d {
	case '{':
		m := map[string]any{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key := foldKey(kt.(string)) // ключ объекта - всегда строка
			if _, dup := m[key]; dup {
				return nil, fmt.Errorf("%w: key %q repeated", errAmbiguousScopes, kt)
			}
			if m[key], err = readStrict(dec); err != nil {
				return nil, err
			}
		}
		_, err := dec.Token() // }
		return m, err
	default: // [
		list := []any{}
		for dec.More() {
			v, err := readStrict(dec)
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
		_, err := dec.Token() // ]
		return list, err
	}
}

// foldKey is the form encoding/json compares field names in: без учета регистра, с
// Юникод-свёрткой ("ID" и "id", "ſcopes" и "scopes" - одно поле).
func foldKey(k string) string {
	return strings.Map(func(r rune) rune {
		lo := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			lo = min(lo, f)
		}
		return lo
	}, k)
}
