// Package usagejsonl keeps model calls (статистика окна DVK) as JSON lines:
// файл рядом с историей чатов, только для своего пользователя (0600).
package usagejsonl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// Uncounted — сервер счет не прислал: in/out неизвестны, а не нули.
	Uncounted bool  `json:"uncounted,omitempty"`
	FirstMS   int64 `json:"first_ms"`
	TookMS    int64 `json:"took_ms"`
	Failed    bool  `json:"failed,omitempty"`
}

// Record appends one call. Под файловым замком, как журнал прогонов: дозапись
// без него теряет строки, когда ответ и разбор портрета пишут разом.
// ponytail: журнал растет без предела (строка ~140 байт на вызов), а экран
// «Статистика» читает его целиком при каждом открытии. Замер 07.11 (#493):
// 100 тыс. вызовов — около трех лет по 90 в день, 13,6 МБ — читаются за
// 0,23 с на M-серии; ротацию по месяцам поэтому не делали. Путь апгрейда,
// когда открытие станет заметно медленным, — файл на месяц и чтение только
// месяцев периода.
func (j Journal) Record(c domain.ModelCall) error {
	if err := os.MkdirAll(filepath.Dir(j.Path), 0o700); err != nil {
		return fmt.Errorf("usage: не создать каталог журнала: %w", err)
	}
	raw, err := json.Marshal(line{At: c.At(), Purpose: string(c.Purpose()), Model: c.Model(), In: c.In(), Out: c.Out(), Uncounted: !c.Counted(),
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

// Load reads every call back for the statistics screen; файла нет — пусто.
// Читает под тем же замком, что и Record: иначе недописанная строка читалась
// бы как битая. Битая строка (обрыв записи, правка руками, строка любой
// длины) пропускается и считается в bad: экран показывает остальное.
func (j Journal) Load() (calls []domain.ModelCall, bad int, err error) {
	if _, err := os.Stat(j.Path); errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	err = filelock.With(j.Path, func() error {
		f, err := os.Open(j.Path)
		if err != nil {
			return fmt.Errorf("usage: не открыть журнал: %w", err)
		}
		defer func() { _ = f.Close() }()
		r := bufio.NewReader(f)
		for {
			raw, rerr := r.ReadBytes('\n')
			if len(bytes.TrimSpace(raw)) > 0 {
				if c, ok := parse(raw); ok {
					calls = append(calls, c)
				} else {
					bad++
				}
			}
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			if rerr != nil {
				return fmt.Errorf("usage: не прочитать журнал: %w", rerr)
			}
		}
	})
	return calls, bad, err
}

// parse reads one journal line; false — строка битая.
func parse(raw []byte) (domain.ModelCall, bool) {
	var l line
	if json.Unmarshal(raw, &l) != nil {
		return domain.ModelCall{}, false
	}
	c, err := domain.NewModelCall(l.At, domain.CallPurpose(l.Purpose), l.Model, l.In, l.Out, !l.Uncounted,
		time.Duration(l.FirstMS)*time.Millisecond, time.Duration(l.TookMS)*time.Millisecond, l.Failed)
	return c, err == nil
}
