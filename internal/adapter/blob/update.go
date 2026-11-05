package blob

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// updateAttempts bounds the read-change-write cycle under conflicts: с запасом
// на всплеск — из шести одновременных писателей один может проиграть пятерым.
const updateAttempts = 10

// conflictPause is the step of the random pause after a lost race: после
// k-го проигрыша — до k шагов. Писатели, столкнувшиеся разом, расходятся, а не
// сталкиваются снова в ту же миллисекунду; сумма пауз — меньше полусекунды.
const conflictPause = 10 * time.Millisecond

// Update reads the object at key, applies change and writes the result under
// the version it read. Если объект успел изменить кто-то другой, он
// перечитывается и change применяется заново к свежему — поэтому change
// получает документ при каждой попытке и решает по нему (номер записи,
// проверка повтора). Ошибка change возвращается как есть, и ничего не пишется.
// После updateAttempts конфликтов подряд — ErrConflict; отмена ctx между
// попытками — ее ошибка.
func Update(ctx context.Context, s Store, key string, change func(raw []byte) ([]byte, error)) error {
	for attempt := range updateAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("write %s: %w", key, ctx.Err())
			case <-time.After(rand.N(time.Duration(attempt) * conflictPause)):
			}
		}
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
