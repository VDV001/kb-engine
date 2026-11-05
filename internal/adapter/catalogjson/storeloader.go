package catalogjson

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/domain"
)

// storeLoadTimeout bounds one Load: у порта CatalogLoader нет ctx.
const storeLoadTimeout = 30 * time.Second

// StoreLoader loads the catalog from a blob.Store and keeps the decoded copy
// while the stored version is unchanged. Сервер перечитывает каталог на каждый
// запрос, как с диска, но из бакета это 2 МБ по сети: здесь каждый Load — один
// условный GET, и неизменный каталог не качается и не разбирается заново.
//
// Один экземпляр *domain.Catalog отдается всем запросам сразу. Это безопасно,
// пока каталог после NewCatalog никто не меняет: Entries и словари отдают
// копии, а Add зовет только конструктор (проверено 05.11.2026).
//
// ponytail: ctx берется из конструктора, а не из запроса, потому что у порта
// Load() его нет. Потолок — отмена запроса клиентом не прерывает чтение
// каталога (не дольше storeLoadTimeout). Путь апгрейда — Load(ctx) в портах
// usecase.
type StoreLoader struct {
	ctx   context.Context
	store blob.Store
	key   string

	mu       sync.Mutex
	etag     string
	cat      *domain.Catalog
	inflight *loadCall
}

// loadCall is one read in flight that later callers wait for.
type loadCall struct {
	done chan struct{}
	cat  *domain.Catalog
	err  error
}

// NewStoreLoader returns a loader of the catalog at key.
func NewStoreLoader(ctx context.Context, store blob.Store, key string) *StoreLoader {
	return &StoreLoader{ctx: ctx, store: store, key: key}
}

// Load returns the current catalog.
//
// Пока одно чтение идет, остальные вызовы ждут его результат, а не стоят в
// очереди каждый со своим GET: страница шлет несколько запросов разом, и без
// этого последний ждал бы сумму их задержек, а при зависшем бакете — сумму
// таймаутов. Цена — ответ ждущего может быть на одно чтение старше его прихода.
//
// Если новая версия не разбирается или каталога больше нет, это ошибка, а не
// прежняя копия: иначе правка, сломавшая каталог, выглядела бы как правка,
// которая не дошла. Сбой связи с бакетом (сеть, 5xx, токен) при уже
// разобранном каталоге отдает его: витрина с диска такого сбоя не знала.
//
// ponytail: отдача прежней копии при сбое бакета нигде не видна. Потолок —
// долгий сбой выглядит как «база не менялась». Путь апгрейда — счетчик в
// /metrics и строка в журнале при первом сбое после успеха.
func (l *StoreLoader) Load() (*domain.Catalog, error) {
	l.mu.Lock()
	if c := l.inflight; c != nil {
		l.mu.Unlock()
		<-c.done
		return c.cat, c.err
	}
	c := &loadCall{done: make(chan struct{})}
	l.inflight = c
	etag, cached := l.etag, l.cat
	l.mu.Unlock()

	cat, newETag, err := l.read(etag, cached)

	l.mu.Lock()
	if err == nil {
		l.etag, l.cat = newETag, cat
	}
	l.inflight = nil
	l.mu.Unlock()
	c.cat, c.err = cat, err
	close(c.done)
	return cat, err
}

// read does one conditional GET against the version etag.
func (l *StoreLoader) read(etag string, cached *domain.Catalog) (*domain.Catalog, string, error) {
	ctx, cancel := context.WithTimeout(l.ctx, storeLoadTimeout)
	defer cancel()
	o, err := l.store.GetIfChanged(ctx, l.key, etag)
	switch {
	case errors.Is(err, blob.ErrNotModified) && cached != nil:
		return cached, etag, nil
	case err != nil && cached != nil && !errors.Is(err, blob.ErrNotFound):
		return cached, etag, nil
	case err != nil:
		return nil, "", fmt.Errorf("load catalog %s: %w", l.key, err)
	}
	cat, err := Decode(bytes.NewReader(o.Data))
	if err != nil {
		return nil, "", fmt.Errorf("catalog %s: %w", l.key, err)
	}
	return cat, o.ETag, nil
}
