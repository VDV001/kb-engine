// Package usagejsonl keeps model calls (статистика окна DVK) as JSON lines:
// файл рядом с историей чатов, только для своего пользователя (0600).
package usagejsonl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/domain"
)

// Journal appends model calls to the file at Path; реализует usage.Recorder.
type Journal struct {
	Path string
}

type line struct {
	At      time.Time `json:"at"`
	Purpose string    `json:"purpose"`
	Model   string    `json:"model"`
	In      int       `json:"in"`
	Out     int       `json:"out"`
	FirstMS int64     `json:"first_ms"`
	TookMS  int64     `json:"took_ms"`
	Failed  bool      `json:"failed,omitempty"`
}

// Record appends one call. Замок тот же, что у журнала прогонов: дозапись
// без него теряет строки, когда ответ и разбор портрета пишут разом.
func (j Journal) Record(c domain.ModelCall) error {
	if err := os.MkdirAll(filepath.Dir(j.Path), 0o700); err != nil {
		return fmt.Errorf("usage: не создать каталог журнала: %w", err)
	}
	raw, err := json.Marshal(line{At: c.At(), Purpose: string(c.Purpose()), Model: c.Model(), In: c.In(), Out: c.Out(),
		FirstMS: c.FirstToken().Milliseconds(), TookMS: c.Took().Milliseconds(), Failed: c.Failed()})
	if err != nil {
		return fmt.Errorf("usage: не закодировать вызов: %w", err)
	}
	return filelock.With(j.Path, func() error {
		f, err := os.OpenFile(j.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("usage: не открыть журнал: %w", err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.Write(append(raw, '\n')); err != nil {
			return fmt.Errorf("usage: не записать вызов: %w", err)
		}
		return f.Close()
	})
}
