package catalogjson

import (
	"context"
	"errors"
	"fmt"

	"github.com/daniil/kb-engine/internal/adapter/blob"
)

// storeWriteAttempts bounds the read-change-write cycle under conflicts.
const storeWriteAttempts = 5

// StoreWriter changes the catalog kept in a blob.Store. Правка применяется к
// той версии, что прочитана, и пишется условно: если каталог успел изменить
// кто-то другой, он перечитывается и правка применяется заново. Поэтому
// change получает свежий документ при каждой попытке — добавление, которое
// выдает номер записи, планирует его по каталогу, в котором номер свободен.
//
// Снимков перед записью, как у файла (filebackup), здесь нет: у бакета
// включены версии, и прежняя копия каталога остается в хранилище.
type StoreWriter struct {
	store blob.Store
	key   string
}

// NewStoreWriter returns a writer of the catalog at key.
func NewStoreWriter(store blob.Store, key string) StoreWriter {
	return StoreWriter{store: store, key: key}
}

// Update reads the catalog, applies change and writes the result under the
// version it read. Ошибка change возвращается как есть, и ничего не пишется.
func (w StoreWriter) Update(ctx context.Context, change func(raw []byte) ([]byte, error)) error {
	for range storeWriteAttempts {
		o, err := w.store.Get(ctx, w.key)
		if err != nil {
			return fmt.Errorf("read catalog %s: %w", w.key, err)
		}
		doc, err := change(o.Data)
		if err != nil {
			return err
		}
		_, err = w.store.Put(ctx, w.key, doc, o.ETag)
		if errors.Is(err, blob.ErrConflict) {
			continue
		}
		if err != nil {
			return fmt.Errorf("write catalog %s: %w", w.key, err)
		}
		return nil
	}
	return fmt.Errorf("catalog %s kept changing under %d attempts: %w", w.key, storeWriteAttempts, blob.ErrConflict)
}
