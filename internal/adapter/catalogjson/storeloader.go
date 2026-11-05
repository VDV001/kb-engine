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

	mu   sync.Mutex
	etag string
	cat  *domain.Catalog
}

// NewStoreLoader returns a loader of the catalog at key.
func NewStoreLoader(ctx context.Context, store blob.Store, key string) *StoreLoader {
	return &StoreLoader{ctx: ctx, store: store, key: key}
}

// Load returns the current catalog. Если новая версия не разбирается, это
// ошибка, а не прежняя копия: иначе правка, сломавшая каталог, выглядела бы
// как правка, которая не дошла.
//
// ponytail: замок держится на время чтения, и параллельные запросы ждут один
// условный GET. Потолок — задержка одного GET на всех; при неизменном каталоге
// это 304 без тела. Путь апгрейда — singleflight.
func (l *StoreLoader) Load() (*domain.Catalog, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx, cancel := context.WithTimeout(l.ctx, storeLoadTimeout)
	defer cancel()
	o, err := l.store.GetIfChanged(ctx, l.key, l.etag)
	if errors.Is(err, blob.ErrNotModified) && l.cat != nil {
		return l.cat, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load catalog %s: %w", l.key, err)
	}
	cat, err := Decode(bytes.NewReader(o.Data))
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", l.key, err)
	}
	l.etag, l.cat = o.ETag, cat
	return cat, nil
}
