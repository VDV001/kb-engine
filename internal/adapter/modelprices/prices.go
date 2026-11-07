// Package modelprices reads model prices from an OpenAI-compatible gateway's
// model list (Vercel AI Gateway отдает pricing.input/output — долларов за
// токен — без ключа) and keeps them in a file for a day: экран «Деньги» (#493)
// не ходит в сеть на каждое открытие и работает без сети по вчерашним ценам.
package modelprices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// maxAge — сколько цены в кэше считаются свежими.
const maxAge = 24 * time.Hour

// retryAfter — пауза после отказа шлюза: без сети «Деньги» не ждут срок
// запроса на каждом открытии.
const retryAfter = 10 * time.Minute

// maxBody — предел ответа шлюза: список ~500 моделей весит ~1 МБ.
const maxBody = 16 << 20

// Cache is the price list of the gateway at BaseURL (…/v1), kept in Path.
type Cache struct {
	Path    string
	BaseURL string
	HTTP    *http.Client // nil — http.DefaultClient; срок задает ctx
	Now     func() time.Time

	mu       sync.Mutex
	failedAt time.Time // последний отказ шлюза; до retryAfter после него в сеть не ходим
}

type price struct {
	In  string `json:"input"`
	Out string `json:"output"`
}

type file struct {
	FetchedAt time.Time        `json:"fetchedAt"`
	Prices    map[string]price `json:"prices"` // долларов за токен, как прислал шлюз
}

// Load returns prices by model id. Кэш свежее суток — без сети; иначе список
// запрашивается заново, а при отказе шлюза служит старый кэш и ошибка
// говорит, почему цены не обновились.
func (c *Cache) Load(ctx context.Context) (map[string]domain.ModelPrice, error) {
	old, readErr := c.read()
	if readErr == nil && c.Now().Sub(old.FetchedAt) < maxAge {
		return book(old), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	if !c.failedAt.IsZero() && c.Now().Sub(c.failedAt) < retryAfter {
		err = errors.New("шлюз недавно не ответил, повтор через несколько минут")
	}
	var fresh file
	if err == nil {
		if fresh, err = c.fetch(ctx); err != nil {
			c.failedAt = c.Now()
		}
	}
	if err != nil {
		if readErr != nil {
			return map[string]domain.ModelPrice{}, fmt.Errorf("цены моделей: %w", err)
		}
		return book(old), fmt.Errorf("цены моделей от %s: шлюз не ответил: %w", old.FetchedAt.Format(time.DateOnly), err)
	}
	if err := c.write(fresh); err != nil {
		return book(fresh), fmt.Errorf("цены моделей не сохранились: %w", err)
	}
	return book(fresh), nil
}

// book parses the kept strings; модель с испорченной ценой остается без цены.
func book(f file) map[string]domain.ModelPrice {
	b := make(map[string]domain.ModelPrice, len(f.Prices))
	for id, p := range f.Prices {
		if mp, err := domain.ParseModelPricePerToken(p.In, p.Out); err == nil {
			b[id] = mp
		}
	}
	return b
}

func (c *Cache) fetch(ctx context.Context) (file, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/models", nil)
	if err != nil {
		return file{}, err
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return file{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return file{}, fmt.Errorf("шлюз ответил %s", resp.Status)
	}
	var list struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing *price `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&list); err != nil {
		return file{}, fmt.Errorf("список моделей не прочитан: %w", err)
	}
	f := file{FetchedAt: c.Now(), Prices: map[string]price{}}
	for _, m := range list.Data {
		if m.ID != "" && m.Pricing != nil && m.Pricing.In != "" && m.Pricing.Out != "" {
			f.Prices[m.ID] = *m.Pricing
		}
	}
	if len(f.Prices) == 0 {
		return file{}, errors.New("в списке моделей нет ни одной цены")
	}
	return f, nil
}

func (c *Cache) read() (file, error) {
	b, err := os.ReadFile(c.Path)
	if err != nil {
		return file{}, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return file{}, err
	}
	return f, nil
}

// write replaces the file atomically: прерванная запись не оставит полфайла.
func (c *Cache) write(f file) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.Path), ".prices-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // после Rename файла уже нет — ошибка ожидаема
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.Path)
}
