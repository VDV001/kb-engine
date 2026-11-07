package kbserver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/daniil/kb-engine/internal/adapter/portraitjson"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/portrait"
)

const portraitPath = "/api/portrait"

// PortraitStore keeps the portrait (#479) on the kb server: реализует
// portrait.Store. Save пишет поверх версии, прочитанной последним Load;
// версию успело сменить другое устройство — portrait.ErrConflict, и сервис
// портрета перечитывает.
type PortraitStore struct {
	c    Client
	mu   sync.Mutex
	etag string // "" — портрета на сервере еще нет
}

// NewPortraitStore returns the portrait store of the person behind c.Token.
func NewPortraitStore(c Client) *PortraitStore { return &PortraitStore{c: c} }

// Load reads the portrait and remembers its version for Save.
func (s *PortraitStore) Load(ctx context.Context) (domain.Portrait, error) {
	p, ver, err := s.LoadVersion(ctx)
	if err != nil {
		return domain.Portrait{}, err
	}
	s.mu.Lock()
	s.etag = ver
	s.mu.Unlock()
	return p, nil
}

// LoadVersion reads the portrait and its version (ETag; "" — портрета еще
// нет): реализует portrait.Versioned. Хранилище версию не запоминает.
func (s *PortraitStore) LoadVersion(ctx context.Context) (domain.Portrait, string, error) {
	target, err := url.JoinPath(s.c.URL, portraitPath)
	if err != nil {
		return domain.Portrait{}, "", fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	resp, err := s.c.send(ctx, http.MethodGet, target, portraitPath, nil, nil)
	if err != nil {
		return domain.Portrait{}, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := statusErr(portraitPath, resp.StatusCode); err != nil {
		return domain.Portrait{}, "", err
	}
	raw, err := readLimited(resp, portraitPath, maxBody)
	if err != nil {
		return domain.Portrait{}, "", err
	}
	p, err := portraitjson.Decode(raw)
	if err != nil {
		// Сервер здоров, но его портрет эта версия не читает (раздел новее и т. п.).
		return domain.Portrait{}, "", fmt.Errorf("%w: портрет на сервере не читается этой версией окна: %w", ErrRejected, err)
	}
	return p, resp.Header.Get("ETag"), nil
}

// Save writes the portrait over the version Load read.
func (s *PortraitStore) Save(ctx context.Context, p domain.Portrait) error {
	s.mu.Lock()
	etag := s.etag
	s.mu.Unlock()
	next, err := s.save(ctx, p, etag)
	if err == nil {
		s.mu.Lock()
		s.etag = next
		s.mu.Unlock()
	}
	return err
}

// SaveVersion writes the portrait only over version (If-Match; "" — портрета
// еще нет): реализует portrait.Versioned. Другая версия — portrait.ErrConflict.
func (s *PortraitStore) SaveVersion(ctx context.Context, p domain.Portrait, version string) error {
	_, err := s.save(ctx, p, version)
	return err
}

// save PUTs the portrait over etag and returns the new version.
func (s *PortraitStore) save(ctx context.Context, p domain.Portrait, etag string) (string, error) {
	raw, err := portraitjson.Encode(p)
	if err != nil {
		return "", err
	}
	target, err := url.JoinPath(s.c.URL, portraitPath)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	h := http.Header{"Content-Type": {"application/json"}}
	if etag == "" {
		h.Set("If-None-Match", "*")
	} else {
		h.Set("If-Match", etag)
	}
	resp, err := s.c.send(ctx, http.MethodPut, target, portraitPath, raw, h)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return resp.Header.Get("ETag"), nil
	case http.StatusPreconditionFailed:
		return "", portrait.ErrConflict
	case http.StatusBadRequest, http.StatusPreconditionRequired:
		msg, _ := readLimited(resp, portraitPath, 1024)
		return "", fmt.Errorf("%w: %s", ErrRejected, strings.TrimSpace(string(msg)))
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return "", fmt.Errorf("%w: %s answered %d — сервер старой версии, без портрета", ErrNotKB, portraitPath, resp.StatusCode)
	}
	if err := statusErr(portraitPath, resp.StatusCode); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%w: %s answered %d", ErrNotKB, portraitPath, resp.StatusCode)
}
