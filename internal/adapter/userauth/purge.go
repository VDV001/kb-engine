package userauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/domain"
)

// ErrNoPurge is returned when the store cannot remove a tree (no blob.Purger).
var ErrNoPurge = errors.New("userauth: the store cannot purge")

// PurgeReport counts the objects Purge took, by place.
type PurgeReport struct {
	Base int // users/<id>/: база, финансы, журнал ящика
	Mail int // mailfiles/users/<id>/: файлы ящика
	Seen int // auth/seen/<id>.json: входы телефонов

	// ScopeKept: запись об области осталась в файле scopes.json, потому что он битый.
	ScopeKept bool
}

// Empty reports whether nothing was removed (запись об области в счет не идет).
func (r PurgeReport) Empty() bool { return r.Base == 0 && r.Mail == 0 && r.Seen == 0 }

// Purge removes a person from the server with their data: вход, телефоны и push
// (Revoke), затем база, файлы ящика и входы телефонов со всеми версиями
// объектов. Повтор безопасен; отозванный раньше снимается тоже (Revoke на него
// отвечает ErrNoSuchUser, и это не отказ).
//
// Хранилище без blob.Purger отказывает ДО отзыва: полдела не остается.
//
// Что НЕ уходит: общие реестры auth/users.json, auth/devices.json и
// auth/push.json правятся без человека, но их прежние версии в бакете хранят
// его имя и хэш токена до правила жизненного цикла (30 дней) — файлы общие с
// другими людьми, версии по одной не вычистить, не задев чужих.
//
// Вызывающий дает другим экземплярам сервера узнать об отзыве (RefreshEvery)
// до вызова: человек с еще живым токеном записал бы в базу после удаления.
func Purge(ctx context.Context, s blob.Store, id domain.UserID) (PurgeReport, error) {
	p, ok := s.(blob.Purger)
	if !ok {
		return PurgeReport{}, ErrNoPurge
	}
	if err := Revoke(ctx, s, id); err != nil && !errors.Is(err, domain.ErrNoSuchUser) {
		return PurgeReport{}, err
	}
	// Запись об области уходит с человеком: имя не остается ограниченным в файле. Битый
	// файл не переписать, и из-за него данные оставлять нельзя: отзыв уже сделан. Запись об
	// области остается (имя при повторном выпуске получит прежнее ограничение, а не снятое).
	var rep PurgeReport
	if err := SetScope(ctx, s, id, domain.ScopeWrite); errors.Is(err, ErrScopesBroken) {
		rep.ScopeKept = true
	} else if err != nil {
		return PurgeReport{}, fmt.Errorf("userauth: purge scope of %s: %w", id, err)
	}
	for _, step := range []struct {
		prefix string
		into   *int
	}{
		{"users/" + id.String() + "/", &rep.Base},
		{"mailfiles/users/" + id.String() + "/", &rep.Mail},
		{"auth/seen/" + id.String() + ".", &rep.Seen}, // «.»: auth/seen/a.json, не ab.json
	} {
		n, err := p.Purge(ctx, step.prefix)
		*step.into = n
		if err != nil {
			return rep, fmt.Errorf("userauth: purge %s: %w", step.prefix, err)
		}
	}
	return rep, nil
}
