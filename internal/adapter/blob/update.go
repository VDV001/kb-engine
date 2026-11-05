package blob

import (
	"context"
	"errors"
	"fmt"
)

// updateAttempts bounds the read-change-write cycle under conflicts.
const updateAttempts = 5

// Update reads the object at key, applies change and writes the result under
// the version it read. Если объект успел изменить кто-то другой, он
// перечитывается и change применяется заново к свежему — поэтому change
// получает документ при каждой попытке и решает по нему (номер записи,
// проверка повтора). Ошибка change возвращается как есть, и ничего не пишется.
// После updateAttempts конфликтов подряд — ErrConflict.
func Update(ctx context.Context, s Store, key string, change func(raw []byte) ([]byte, error)) error {
	for range updateAttempts {
		o, err := s.Get(ctx, key)
		if err != nil {
			return fmt.Errorf("read %s: %w", key, err)
		}
		doc, err := change(o.Data)
		if err != nil {
			return err
		}
		_, err = s.Put(ctx, key, doc, o.ETag)
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return fmt.Errorf("write %s: %w", key, err)
		}
		return nil
	}
	return fmt.Errorf("%s kept changing under %d attempts: %w", key, updateAttempts, ErrConflict)
}
