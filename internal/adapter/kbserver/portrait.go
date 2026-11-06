package kbserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// Load reads the portrait and remembers its version.
func (s *PortraitStore) Load(ctx context.Context) (domain.Portrait, error) {
	target, err := url.JoinPath(s.c.URL, portraitPath)
	if err != nil {
		return domain.Portrait{}, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	resp, err := s.c.send(ctx, http.MethodGet, target, portraitPath, nil, nil)
	if err != nil {
		return domain.Portrait{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := statusErr(portraitPath, resp.StatusCode); err != nil {
		return domain.Portrait{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return domain.Portrait{}, fmt.Errorf("%w: %s: %w", ErrUnreachable, portraitPath, err)
	}
	if len(raw) > maxBody {
		return domain.Portrait{}, fmt.Errorf("%w: %s больше %d байт", ErrTooLarge, portraitPath, maxBody)
	}
	p, err := portraitjson.Decode(raw)
	if err != nil {
		return domain.Portrait{}, err
	}
	s.mu.Lock()
	s.etag = resp.Header.Get("ETag")
	s.mu.Unlock()
	return p, nil
}

// Save writes the portrait over the version Load read.
func (s *PortraitStore) Save(ctx context.Context, p domain.Portrait) error {
	raw, err := portraitjson.Encode(p)
	if err != nil {
		return err
	}
	target, err := url.JoinPath(s.c.URL, portraitPath)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	s.mu.Lock()
	etag := s.etag
	s.mu.Unlock()
	h := http.Header{"Content-Type": {"application/json"}}
	if etag == "" {
		h.Set("If-None-Match", "*")
	} else {
		h.Set("If-Match", etag)
	}
	resp, err := s.c.send(ctx, http.MethodPut, target, portraitPath, raw, h)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusNoContent:
		s.mu.Lock()
		s.etag = resp.Header.Get("ETag")
		s.mu.Unlock()
		return nil
	case http.StatusPreconditionFailed:
		return portrait.ErrConflict
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return fmt.Errorf("%w: %s answered %d — сервер старой версии, без портрета", ErrNotKB, portraitPath, resp.StatusCode)
	}
	if err := statusErr(portraitPath, resp.StatusCode); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s answered %d", ErrNotKB, portraitPath, resp.StatusCode)
}
