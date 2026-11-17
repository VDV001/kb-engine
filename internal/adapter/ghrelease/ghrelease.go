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

// Fetch reads Base+path; больше limit байт — ErrTooLarge.
func (c Client) Fetch(ctx context.Context, path string, limit int64) ([]byte, error) {
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release %s: %s", path, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%w: %s over %d bytes", ErrTooLarge, path, limit)
	}
	return b, nil
}

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
