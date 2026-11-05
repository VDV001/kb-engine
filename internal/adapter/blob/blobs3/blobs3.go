// Package blobs3 is a blob.Store on Yandex Object Storage through its S3 API.
//
// Запросы идут с IAM-токеном (`Authorization: Bearer`), который Object Storage
// принимает вместо подписи SigV4: статических ключей доступа нет вовсе, и
// утечь нечему, кроме токена, живущего часы. В Serverless Container токен
// сервисного аккаунта отдает сервис метаданных (MetadataToken).
//
// Условная запись — документированные PutObject заголовки If-None-Match: * и
// If-Match: <ETag>, при невыполненном условии 412.
package blobs3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/loopback"
)

// YandexEndpoint is the Object Storage S3 API endpoint.
const YandexEndpoint = "https://storage.yandexcloud.net"

// requestTimeout bounds one request; объект до 64 МБ укладывается с запасом.
const requestTimeout = 2 * time.Minute

// TokenSource supplies the IAM token for each request.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken is a fixed token: тесты и разовый перенос данных с Мака
// (`yc iam create-token`).
type StaticToken string

// Token returns the token.
func (t StaticToken) Token(context.Context) (string, error) { return string(t), nil }

// Config describes the bucket.
type Config struct {
	// Endpoint is the S3 API base URL, без пути. http допустим только на
	// loopback (тесты, эмулятор): по сети токен ушел бы открытым текстом.
	Endpoint string
	Bucket   string
	// Prefix is prepended to every key; пустой или оканчивается на «/».
	// Нужен живому тесту, чтобы прогон не трогал чужих ключей.
	Prefix string
	Token  TokenSource
	// Client defaults to one with requestTimeout that never follows redirects.
	Client *http.Client
}

// Store is a blob.Store on one bucket.
type Store struct {
	base   string // endpoint + "/" + bucket + "/"
	prefix string
	token  TokenSource
	client *http.Client
}

var _ blob.Store = (*Store)(nil)

// New validates cfg and returns the store.
func New(cfg Config) (*Store, error) {
	u, err := parseEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	if !validBucket(cfg.Bucket) {
		return nil, fmt.Errorf("blobs3: bucket %q: 3–63 символа из a-z, 0-9, точки и дефиса", cfg.Bucket)
	}
	if err := validPrefix(cfg.Prefix); err != nil {
		return nil, err
	}
	if cfg.Token == nil {
		return nil, errors.New("blobs3: no token source")
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{
			Timeout: requestTimeout,
			// Перенаправление унесло бы токен туда, куда его не посылали.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &Store{
		base:   u.Scheme + "://" + u.Host + "/" + cfg.Bucket + "/",
		prefix: cfg.Prefix,
		token:  cfg.Token,
		client: client,
	}, nil
}

// parseEndpoint accepts only scheme and host: https, или http на loopback.
func parseEndpoint(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("blobs3: endpoint: %w", err)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && loopback.Host(u.Hostname()):
	default:
		return nil, fmt.Errorf("blobs3: endpoint %q: нужен https (http только на 127.0.0.1)", endpoint)
	}
	if u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return nil, fmt.Errorf("blobs3: endpoint %q: только схема и хост", endpoint)
	}
	return u, nil
}

// validPrefix accepts "" or a valid key followed by «/».
func validPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	trimmed, ok := strings.CutSuffix(prefix, "/")
	if !ok {
		return fmt.Errorf("blobs3: prefix %q must end with /", prefix)
	}
	if err := blob.ValidKey(trimmed); err != nil {
		return fmt.Errorf("blobs3: prefix: %w", err)
	}
	return nil
}

// validBucket checks the S3 bucket naming rule (без проверки на вид IP-адреса):
// 3–63 символа из a-z, 0-9, точки и дефиса, по краям буква или цифра, без «..».
func validBucket(b string) bool {
	if len(b) < 3 || len(b) > 63 || strings.Contains(b, "..") ||
		!alnum(rune(b[0])) || !alnum(rune(b[len(b)-1])) {
		return false
	}
	for _, r := range b {
		if !alnum(r) && r != '.' && r != '-' {
			return false
		}
	}
	return true
}

func alnum(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') }

// s3KeyLimit is the S3 limit on a full key, prefix included, in bytes.
const s3KeyLimit = 1024

// checkKey is blob.ValidKey plus the S3 limit on the key with the prefix.
func (s *Store) checkKey(key string) error {
	if err := blob.ValidKey(key); err != nil {
		return err
	}
	if n := len(s.prefix) + len(key); n > s3KeyLimit {
		return fmt.Errorf("%w: %d bytes with the prefix, S3 allows %d", blob.ErrInvalidKey, n, s3KeyLimit)
	}
	return nil
}

// objectURL escapes each segment of the key: кириллица и пробелы в именах
// заметок едут в процентной записи, «/» остается разделителем.
func (s *Store) objectURL(key string) string {
	segs := strings.Split(s.prefix+key, "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	return s.base + strings.Join(segs, "/")
}

// Get reads the object at key.
func (s *Store) Get(ctx context.Context, key string) (blob.Object, error) {
	if err := s.checkKey(key); err != nil {
		return blob.Object{}, err
	}
	resp, err := s.do(ctx, http.MethodGet, key, nil, nil)
	if err != nil {
		return blob.Object{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return blob.Object{}, fmt.Errorf("%w: %s (404)", blob.ErrNotFound, key)
	default:
		return blob.Object{}, statusError("get", key, resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, blob.MaxObjectSize+1))
	if err != nil {
		return blob.Object{}, fmt.Errorf("blobs3: get %s: %w", key, err)
	}
	if len(data) > blob.MaxObjectSize {
		return blob.Object{}, fmt.Errorf("%w: %s", blob.ErrTooLarge, key)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return blob.Object{}, fmt.Errorf("blobs3: get %s: no ETag in the answer", key)
	}
	return blob.Object{Data: data, ETag: etag}, nil
}

// Put writes data at key under the ifMatch condition (see blob.Store).
func (s *Store) Put(ctx context.Context, key string, data []byte, ifMatch string) (string, error) {
	if err := s.checkKey(key); err != nil {
		return "", err
	}
	if len(data) > blob.MaxObjectSize {
		return "", fmt.Errorf("%w: %d bytes", blob.ErrTooLarge, len(data))
	}
	cond := http.Header{}
	if ifMatch == "" {
		cond.Set("If-None-Match", "*")
	} else {
		cond.Set("If-Match", ifMatch)
	}
	resp, err := s.do(ctx, http.MethodPut, key, data, cond)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	// 412 — условие не выполнено; 409 — ключ изменила параллельная запись во
	// время загрузки; 404 при If-Match — ключ удален. Для писателя все три —
	// «версия не та, что я читал».
	case http.StatusPreconditionFailed, http.StatusConflict:
		return "", fmt.Errorf("%w: %s (%d)", blob.ErrConflict, key, resp.StatusCode)
	case http.StatusNotFound:
		if ifMatch != "" {
			return "", fmt.Errorf("%w: %s is gone (404)", blob.ErrConflict, key)
		}
		return "", statusError("put", key, resp)
	default:
		return "", statusError("put", key, resp)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("blobs3: put %s: no ETag in the answer", key)
	}
	return etag, nil
}

func (s *Store) do(ctx context.Context, method, key string, body []byte, h http.Header) (*http.Response, error) {
	tok, err := s.token.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("blobs3: token: %w", err)
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.objectURL(key), rd)
	if err != nil {
		return nil, fmt.Errorf("blobs3: %w", err)
	}
	maps.Copy(req.Header, h)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("blobs3: %s %s: %w", strings.ToLower(method), key, err)
	}
	return resp, nil
}

// statusError names the status and the S3 error code, но не тело целиком: в
// теле ответа об ошибке бывает эхо запроса.
func statusError(op, key string, resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	code := ""
	if _, rest, ok := bytes.Cut(raw, []byte("<Code>")); ok {
		if c, _, ok := bytes.Cut(rest, []byte("</Code>")); ok && len(c) <= 64 {
			code = " " + string(c)
		}
	}
	return fmt.Errorf("blobs3: %s %s: status %d%s", op, key, resp.StatusCode, code)
}

// MetadataToken fetches the service account's IAM token from the metadata
// service of a Serverless Container (or a VM) and caches it until shortly
// before it expires.
type MetadataToken struct {
	// BaseURL defaults to http://169.254.169.254.
	BaseURL string
	// Client defaults to one with a short timeout.
	Client *http.Client
	// Now defaults to time.Now.
	Now func() time.Time

	mu      sync.Mutex
	tok     string
	refresh time.Time
	expiry  time.Time
}

const (
	metadataBase = "http://169.254.169.254"
	metadataPath = "/computeMetadata/v1/instance/service-accounts/default/token"
	// refreshMargin: токен берется заново за столько до конца срока, чтобы
	// запрос не ушел с токеном, истекшим по дороге.
	refreshMargin = 5 * time.Minute
)

// metadataClient never uses a proxy: ответ несет токен сервисного аккаунта
// открытым текстом, а http.DefaultTransport слушается HTTP_PROXY.
var metadataClient = &http.Client{
	Timeout:   5 * time.Second,
	Transport: &http.Transport{Proxy: nil},
}

// Token returns a cached token or fetches a new one. Если обновление не
// удалось, а прежний токен еще не истек, отдается он: сбой сервиса метаданных
// не роняет запросы раньше, чем это сделал бы сам срок токена.
//
// ponytail: замок держится на время запроса к метаданным, и ждущие вызовы не
// отменяются своим ctx. Потолок — при недоступных метаданных и истекшем токене
// N параллельных запросов ждут друг за другом до 5 с каждый; в окне обновления
// их выручает кэш выше. Путь апгрейда — singleflight с ожиданием по ctx.
func (m *MetadataToken) Token(ctx context.Context) (string, error) {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tok != "" && now().Before(m.refresh) {
		return m.tok, nil
	}
	tok, ttl, err := m.fetch(ctx)
	if err != nil {
		if m.tok != "" && now().Before(m.expiry) {
			return m.tok, nil
		}
		return "", err
	}
	at := now()
	m.tok, m.refresh, m.expiry = tok, at.Add(ttl-refreshMargin), at.Add(ttl)
	return tok, nil
}

func (m *MetadataToken) fetch(ctx context.Context) (string, time.Duration, error) {
	base := m.BaseURL
	if base == "" {
		base = metadataBase
	}
	client := m.Client
	if client == nil {
		client = metadataClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+metadataPath, nil)
	if err != nil {
		return "", 0, fmt.Errorf("metadata token: %w", err)
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("metadata token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("metadata token: status %d", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return "", 0, fmt.Errorf("metadata token: %w", err)
	}
	ttl := time.Duration(body.ExpiresIn) * time.Second
	if body.AccessToken == "" || ttl <= refreshMargin {
		return "", 0, errors.New("metadata token: empty token or lifetime too short")
	}
	return body.AccessToken, ttl, nil
}

func (s *Store) GetIfChanged(context.Context, string, string) (blob.Object, error) {
	return blob.Object{}, errors.New("not implemented")
}
