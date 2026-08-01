// Package linkcheck asks a URL for its HTTP status. It implements the drift
// scan's LinkChecker port.
package linkcheck

import (
	"fmt"
	"net/http"
	"time"
)

// Checker performs one HEAD request per URL, pausing Delay between them.
//
// The pause is not politeness boilerplate: a scan of the live catalog is 1313
// requests, most to one host, and sending them at full speed is how a scanner
// earns the 403 it then records as a fact about an article.
type Checker struct {
	Client  *http.Client
	Delay   time.Duration
	lastHit time.Time
}

// New returns a Checker with a bounded timeout and a conservative delay.
func New(timeout, delay time.Duration) *Checker {
	return &Checker{
		Client: &http.Client{
			Timeout: timeout,
			// A redirect is an answer worth recording as such: following it
			// would report 200 for a URL the catalog no longer names correctly.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		Delay: delay,
	}
}

// Head returns the status code for url.
func (c *Checker) Head(url string) (int, error) {
	if !c.lastHit.IsZero() {
		if wait := c.Delay - time.Since(c.lastHit); wait > 0 {
			time.Sleep(wait)
		}
	}
	c.lastHit = time.Now()

	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	// Without a User-Agent many hosts answer 403 to the default Go client, and
	// that 403 would be recorded as a fact about the article rather than about
	// the request.
	req.Header.Set("User-Agent", "kbengine-drift/1.0 (+https://github.com/VDV001/kb-engine)")

	resp, err := c.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}
