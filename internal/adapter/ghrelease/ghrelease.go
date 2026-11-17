// Package ghrelease reads files of the repository's GitHub Releases (#651):
// манифест выпуска, его подпись и сборки окна. Доверие здесь не решается —
// подпись и sha256 проверяет usecase/selfupdate; адаптер только не дает уйти
// на чужой путь, на http и прочитать больше заказанного.
package ghrelease

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

var (
	// ErrTooLarge — файл длиннее, чем обещал манифест или потолок вызова.
	ErrTooLarge = errors.New("release file is over the limit")
	// ErrBadPath — путь не из голых имен: «..», «/» в начале или адрес.
	ErrBadPath = errors.New("release path is not valid")
)

// Releases is the DVK repository's releases page.
const Releases = "https://github.com/VDV001/kb-engine/releases/"

// Client fetches by path under Base (Releases в окне).
type Client struct {
	Base string
	HTTP *http.Client // nil — http.DefaultClient; срок задает ctx
}

var releasePath = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// Fetch reads Base+path whole; больше limit байт — ErrTooLarge.
func (c Client) Fetch(ctx context.Context, path string, limit int64) ([]byte, error) {
	rc, err := c.Open(ctx, path, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// Open streams Base+path; чтение дальше limit байт — ErrTooLarge.
func (c Client) Open(ctx context.Context, path string, limit int64) (io.ReadCloser, error) {
	if !releasePath.MatchString(path) || slices.ContainsFunc(strings.Split(path, "/"), func(s string) bool { return s == "." || s == ".." }) {
		return nil, fmt.Errorf("%w: %q", ErrBadPath, path)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return nil, err
	}
	if req.URL.Scheme != "https" {
		return nil, fmt.Errorf("release %s: only https", req.URL.Redacted())
	}
	hc := c.client()
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("release %s: %s", path, resp.Status)
	}
	return &limited{rc: resp.Body, left: limit, path: path}, nil
}

// limited reads at most left bytes; дальше — ErrTooLarge, а не тихая обрезка.
type limited struct {
	rc   io.ReadCloser
	left int64
	path string
}

func (l *limited) Read(p []byte) (int, error) {
	if l.left <= 0 {
		var one [1]byte
		if n, _ := l.rc.Read(one[:]); n > 0 {
			return 0, fmt.Errorf("%w: %s", ErrTooLarge, l.path)
		}
		return 0, io.EOF
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.rc.Read(p)
	l.left -= int64(n)
	return n, err
}

func (l *limited) Close() error { return l.rc.Close() }

// client is the caller's client with the release redirect policy on top.
func (c Client) client() http.Client {
	hc := http.Client{}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	// GitHub переадресует файл на свое хранилище; только https и недолго.
	// Политика вызывающего остается в силе поверх этой (ревью #672).
	own := hc.CheckRedirect
	hc.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" || len(via) >= 5 {
			return fmt.Errorf("release redirect to %s refused", r.URL.Redacted())
		}
		if own != nil {
			return own(r, via)
		}
		return nil
	}
	return hc
}
