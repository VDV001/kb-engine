package catalogjson

import (
	"context"

	"github.com/daniil/kb-engine/internal/adapter/blob"
)

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
// version it read (blob.Update). Ошибка change возвращается как есть, и ничего
// не пишется.
func (w StoreWriter) Update(ctx context.Context, change func(raw []byte) ([]byte, error)) error {
	return blob.Update(ctx, w.store, w.key, change)
}
