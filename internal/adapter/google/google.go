// Package google is the window's access to Google Drive on the person's
// behalf: вход OAuth для настольных приложений (PKCE, ответ на 127.0.0.1) с
// правом только читать Диск, и чтение документа по id — документы Google
// экспортом в текст, остальное байтами. Без SDK: несколько запросов HTTP.
// Токены хранит вызывающий (Связка ключей); сюда они приходят аргументами.
package google

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// ScopeDriveReadonly — единственное право, которое просит окно.
const ScopeDriveReadonly = "https://www.googleapis.com/auth/drive.readonly"

// Отказы: окно называет их словами человека.
var (
	ErrReconnect = errors.New("google access expired or was revoked")
	ErrNoAccess  = errors.New("no access to the document")
	ErrNotFound  = errors.New("document not found")
	ErrServer    = errors.New("google did not answer")
	ErrTooBig    = errors.New("document is bigger than 20 MB")
)

// maxBody caps one document: как у ссылок — 20 МБ.
const maxBody = 20 << 20

// Client talks to Google. Пустые адреса — настоящие адреса Google.
type Client struct {
	ClientID, ClientSecret string
	AuthBase               string // https://accounts.google.com/o/oauth2/v2/auth
	TokenURL               string // https://oauth2.googleapis.com/token
	APIURL                 string // https://www.googleapis.com/drive/v3
	HTTP                   *http.Client
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func or(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// Challenge is the PKCE S256 challenge of verifier.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthURL is the consent page: только чтение Диска, refresh-токен (offline,
// consent), PKCE и state против подмены ответа.
func (c Client) AuthURL(redirect, state, verifier string) string {
	q := url.Values{
		"client_id": {c.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"},
		"scope": {ScopeDriveReadonly}, "state": {state},
		"code_challenge": {Challenge(verifier)}, "code_challenge_method": {"S256"},
		"access_type": {"offline"}, "prompt": {"consent"},
	}
	return or(c.AuthBase, "https://accounts.google.com/o/oauth2/v2/auth") + "?" + q.Encode()
}

// Tokens is what the token endpoint gave.
type Tokens struct{ Access, Refresh string }

// Exchange trades the code from the consent page for tokens.
func (c Client) Exchange(ctx context.Context, code, redirect, verifier string) (Tokens, error) {
	var t struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	err := c.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}, &t)
	return Tokens{Access: t.Access, Refresh: t.Refresh}, err
}

// Refresh returns a fresh access token.
func (c Client) Refresh(ctx context.Context, refresh string) (string, error) {
	var t struct {
		Access string `json:"access_token"`
	}
	err := c.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}, &t)
	return t.Access, err
}

func (c Client) token(ctx context.Context, form url.Values, out any) error {
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, or(c.TokenURL, "https://oauth2.googleapis.com/token"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrServer, err)
	}
	defer func() { _ = res.Body.Close() }()
	// Текст ответа с ошибкой не берется: в нем бывает эхо запроса с секретом.
	switch {
	case res.StatusCode == http.StatusBadRequest || res.StatusCode == http.StatusUnauthorized:
		return ErrReconnect
	case res.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: status %d", ErrServer, res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("%w: %w", ErrServer, err)
	}
	return nil
}

var (
	docPath    = regexp.MustCompile(`^/(?:document|spreadsheets|presentation)/d/([\w-]+)`)
	filePath   = regexp.MustCompile(`^/file/d/([\w-]+)`)
	folderPath = regexp.MustCompile(`^/drive/(?:u/\d+/)?folders/([\w-]+)`)
	idParam    = regexp.MustCompile(`^[\w-]+$`)
)

// FileID returns the Drive id of a Google link (документ, таблица,
// презентация, файл или папка Диска); "" — ссылка не на Google.
func FileID(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" {
		return ""
	}
	var m []string
	switch u.Host {
	case "docs.google.com":
		m = docPath.FindStringSubmatch(u.Path)
	case "drive.google.com":
		if m = filePath.FindStringSubmatch(u.Path); m == nil {
			m = folderPath.FindStringSubmatch(u.Path)
		}
		if id := u.Query().Get("id"); m == nil && u.Path == "/open" && idParam.MatchString(id) {
			return id
		}
	}
	if m == nil {
		return ""
	}
	return m[1]
}

// Doc is a Drive document: Text — для документов Google, Raw и Ext — для
// файлов, которые распознает окно (pdf, docx, картинки).
type Doc struct {
	ID, Name, MimeType, Text, Ext string
	Raw                           []byte
}

var exportAs = map[string]string{
	"application/vnd.google-apps.document":     "text/plain",
	"application/vnd.google-apps.spreadsheet":  "text/csv",
	"application/vnd.google-apps.presentation": "text/plain",
}

var extOf = map[string]string{
	"application/pdf": "pdf", "image/png": "png", "image/jpeg": "jpg", "image/heic": "heic",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
}

// Read returns the document id with access token at.
func (c Client) Read(ctx context.Context, at, id string) (Doc, error) {
	var meta struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		MimeType string `json:"mimeType"`
	}
	if err := c.getJSON(ctx, at, "/files/"+url.PathEscape(id), url.Values{"fields": {"id,name,mimeType"}, "supportsAllDrives": {"true"}}, &meta); err != nil {
		return Doc{}, err
	}
	d := Doc{ID: meta.ID, Name: meta.Name, MimeType: meta.MimeType}
	if mime, ok := exportAs[meta.MimeType]; ok {
		raw, err := c.get(ctx, at, "/files/"+url.PathEscape(id)+"/export", url.Values{"mimeType": {mime}})
		d.Text = string(raw)
		return d, err
	}
	if d.Ext = extOf[meta.MimeType]; d.Ext == "" && !strings.HasPrefix(meta.MimeType, "text/") {
		return Doc{}, fmt.Errorf("%w: %s не документ (%s)", ErrNoAccess, meta.Name, meta.MimeType)
	}
	raw, err := c.get(ctx, at, "/files/"+url.PathEscape(id), url.Values{"alt": {"media"}, "supportsAllDrives": {"true"}})
	if d.Ext == "" {
		d.Text = string(raw)
	} else {
		d.Raw = raw
	}
	return d, err
}

// Account returns the e-mail of who signed in.
func (c Client) Account(ctx context.Context, at string) (string, error) {
	var a struct {
		User struct {
			Email string `json:"emailAddress"`
		} `json:"user"`
	}
	err := c.getJSON(ctx, at, "/about", url.Values{"fields": {"user(emailAddress,displayName)"}}, &a)
	return a.User.Email, err
}

func (c Client) getJSON(ctx context.Context, at, path string, q url.Values, out any) error {
	raw, err := c.get(ctx, at, path, q)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: %w", ErrServer, err)
	}
	return nil
}

func (c Client) get(ctx context.Context, at, path string, q url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, or(c.APIURL, "https://www.googleapis.com/drive/v3")+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+at)
	res, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServer, err)
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, ErrReconnect
	case http.StatusForbidden:
		return nil, ErrNoAccess
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("%w: status %d", ErrServer, res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServer, err)
	}
	if len(raw) > maxBody {
		return nil, ErrTooBig
	}
	return raw, nil
}
