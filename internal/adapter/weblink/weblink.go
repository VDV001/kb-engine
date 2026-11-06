// Package weblink reads a document by its link for the chat: страница — ее
// текстом, документ Google — его экспортом, PDF и docx — байтами для
// распознавания окном. Ссылку дает человек (поле «+» или текст вопроса), а
// не модель; текст живет в памяти на время ответа и на диск не пишется.
package weblink

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Отказы: окно называет их словами человека.
var (
	ErrBadURL   = errors.New("not an http or https link")
	ErrClosed   = errors.New("document is closed to this window")
	ErrNotFound = errors.New("nothing at the link")
	ErrTooBig   = errors.New("document at the link is too big")
	ErrNotText  = errors.New("link is not a document")
	ErrServer   = errors.New("site did not answer")
)

// DefaultMaxBytes caps what is read from one link.
const DefaultMaxBytes = 20 << 20

// Page is a document read by its link. Text пусто, когда документ — файл
// для распознавания (Ext: pdf, docx, картинка), тогда он в Raw.
type Page struct {
	URL, Title, Text, Ext string
	Raw                   []byte
}

// Client fetches links. LoginHosts — куда уводят закрытые документы (у Google
// это страница входа); nil — accounts.google.com.
type Client struct {
	HTTP       *http.Client // nil — http.DefaultClient; срок задает ctx
	MaxBytes   int64        // 0 — DefaultMaxBytes
	LoginHosts []string
}

var (
	googleDoc   = regexp.MustCompile(`^https://docs\.google\.com/(document|spreadsheets|presentation)/d/([\w-]+)`)
	googleFile  = regexp.MustCompile(`^https://drive\.google\.com/file/d/([\w-]+)`)
	googleGid   = regexp.MustCompile(`gid=(\d+)`)
	linkInText  = regexp.MustCompile(`https?://[^\s<>"«»]+`)
	trailPunct  = ".,;:!?)»'\""
	fileByMIME  = map[string]string{"application/pdf": "pdf", "application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx", "image/png": "png", "image/jpeg": "jpg", "image/heic": "heic", "image/tiff": "tiff"}
	skipElement = map[string]bool{"script": true, "style": true, "noscript": true, "nav": true, "template": true, "svg": true}
	blockBreak  = map[string]bool{"p": true, "div": true, "br": true, "li": true, "tr": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "section": true, "article": true, "table": true, "pre": true, "blockquote": true}
)

// ExportURL turns a link to a Google document into its plain export:
// страница редактора — скрипты, а не документ. Остальные ссылки — как есть.
func ExportURL(link string) string {
	if m := googleDoc.FindStringSubmatch(link); m != nil {
		base := "https://docs.google.com/" + m[1] + "/d/" + m[2]
		switch m[1] {
		case "document":
			return base + "/export?format=txt"
		case "spreadsheets":
			if g := googleGid.FindStringSubmatch(link); g != nil {
				return base + "/export?format=csv&gid=" + g[1]
			}
			return base + "/export?format=csv"
		default:
			return base + "/export/txt"
		}
	}
	if m := googleFile.FindStringSubmatch(link); m != nil {
		return "https://drive.google.com/uc?export=download&id=" + m[1]
	}
	return link
}

// Links returns the links in a question, in order, without repeats and
// without the punctuation that ends the sentence around them.
func Links(text string) []string {
	var out []string
	for _, l := range linkInText.FindAllString(text, -1) {
		l = strings.TrimRight(l, trailPunct)
		if !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}

// Fetch reads the document at link.
func (c Client) Fetch(ctx context.Context, link string) (Page, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Page{}, ErrBadURL
	}
	res, err := c.get(ctx, ExportURL(u.String()))
	if err != nil {
		return Page{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if err := c.statusErr(res); err != nil {
		return Page{}, err
	}
	limit := c.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return Page{}, fmt.Errorf("%w: %w", ErrServer, err)
	}
	if int64(len(raw)) > limit {
		return Page{}, ErrTooBig
	}
	return page(link, res, raw)
}

func (c Client) get(ctx context.Context, link string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, ErrBadURL
	}
	req.Header.Set("User-Agent", "DVK")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServer, err)
	}
	return res, nil
}

// statusErr names a refusal: закрытый документ — и ответом 401/403, и уводом
// на страницу входа.
func (c Client) statusErr(res *http.Response) error {
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden || c.isLogin(res.Request.URL):
		return ErrClosed
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		return ErrNotFound
	case res.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: status %d", ErrServer, res.StatusCode)
	}
	return nil
}

func (c Client) isLogin(u *url.URL) bool {
	hosts := c.LoginHosts
	if hosts == nil {
		hosts = []string{"accounts.google.com"}
	}
	return slices.Contains(hosts, u.Host)
}

// page reads the body by its type.
func page(link string, res *http.Response, raw []byte) (Page, error) {
	p := Page{URL: link, Title: nameOf(res.Request.URL, res.Header.Get("Content-Disposition"))}
	ct, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	switch {
	case ct == "text/html" || ct == "application/xhtml+xml":
		title, text := htmlText(raw)
		if title != "" {
			p.Title = title
		}
		p.Text = text
	case strings.HasPrefix(ct, "text/") || ct == "application/json" || ct == "application/xml":
		if !utf8.Valid(raw) {
			return Page{}, ErrNotText
		}
		p.Text = string(raw)
	case fileByMIME[ct] != "":
		p.Ext, p.Raw = fileByMIME[ct], raw
	default:
		return Page{}, fmt.Errorf("%w: %s", ErrNotText, ct)
	}
	return p, nil
}

// nameOf names the document: имя файла из ответа, иначе последний кусок пути, иначе сайт.
func nameOf(u *url.URL, disposition string) string {
	if _, params, err := mime.ParseMediaType(disposition); err == nil && params["filename"] != "" {
		return params["filename"]
	}
	if b := path.Base(u.Path); b != "/" && b != "." && b != "" {
		return b
	}
	return u.Host
}

// htmlText returns the title and the readable text of a page: без скриптов,
// стилей и меню; блоки — с новой строки, ячейки — через пробел.
func htmlText(raw []byte) (title, text string) {
	z := html.NewTokenizer(bytes.NewReader(raw))
	w := &textWriter{}
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return strings.TrimSpace(w.title.String()), tidy(w.b.String())
		case html.StartTagToken, html.EndTagToken:
			name, _ := z.TagName()
			w.tag(string(name), tt == html.EndTagToken)
		case html.SelfClosingTagToken: // <svg/> — начало и конец сразу
			name, _ := z.TagName()
			w.tag(string(name), false)
			w.tag(string(name), true)
		case html.TextToken:
			w.text(string(z.Text()))
		}
	}
}

// textWriter collects the text of a page tag by tag.
type textWriter struct {
	b, title strings.Builder
	skip     int
	inTitle  bool
}

func (w *textWriter) tag(name string, end bool) {
	if name == "title" {
		w.inTitle = !end
	}
	if skipElement[name] {
		if !end {
			w.skip++
		} else if w.skip > 0 {
			w.skip--
		}
	}
	if blockBreak[name] {
		w.b.WriteByte('\n')
	} else if !end && (name == "td" || name == "th") {
		w.b.WriteByte(' ')
	}
}

func (w *textWriter) text(t string) {
	switch {
	case w.inTitle:
		w.title.WriteString(t)
	case w.skip == 0:
		w.b.WriteString(t)
	}
}

// tidy collapses spaces in each line and drops empty lines.
func tidy(s string) string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if f := strings.Join(strings.Fields(strings.ReplaceAll(line, " ", " ")), " "); f != "" {
			out = append(out, f)
		}
	}
	return strings.Join(out, "\n")
}
