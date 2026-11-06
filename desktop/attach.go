package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/google"
	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/adapter/weblink"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// Приложенное к вопросу (#436): «+» в поле чата — файл с диска, документ из
// найденного, ссылка — и ссылки, вставленные в текст вопроса. Текст живет в
// памяти на время ответа; в файл чата попадают адрес и название.

// maxAttached caps what one question carries: приложенное идет модели целиком.
const maxAttached = 5

// maxDiskFile caps a file attached from disk.
const maxDiskFile = 20 << 20

// attachRefs joins the attached refs and the links of the question, without
// repeats, in the order the person gave them.
func attachRefs(refs []string, question string) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if r = strings.TrimSpace(r); r != "" && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	for _, l := range weblink.Links(question) {
		if r := "url:" + l; !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// readAttached reads every ref; первый отказ останавливает вопрос: человек
// приложил документ, чтобы модель его прочла, и ответ без него был бы не тем.
func (d *chatDesk) readAttached(ctx context.Context, refs []string) ([]chat.Doc, error) {
	if len(refs) > maxAttached {
		return nil, fmt.Errorf("к вопросу приложено %d документов — не больше %d за раз", len(refs), maxAttached)
	}
	docs := make([]chat.Doc, 0, len(refs))
	for _, ref := range refs {
		if d.attach == nil {
			return nil, errors.New("приложить документ можно только в окне приложения")
		}
		doc, err := d.attach(ctx, ref)
		if err != nil {
			return nil, attachErr(ref, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// attachErr says which document was not read and what to do.
func attachErr(ref string, err error) error {
	what := ref
	if l, ok := strings.CutPrefix(ref, "url:"); ok {
		what = l
	} else if p, ok := strings.CutPrefix(ref, "file:"); ok {
		what = filepath.Base(p)
	}
	switch {
	case errors.Is(err, weblink.ErrClosed):
		return fmt.Errorf("документ закрыт: %s — откройте доступ по ссылке или приложите файл", what)
	case errors.Is(err, weblink.ErrNotFound):
		return fmt.Errorf("по ссылке ничего нет: %s", what)
	case errors.Is(err, weblink.ErrTooBig):
		return fmt.Errorf("документ больше 20 МБ: %s", what)
	case errors.Is(err, weblink.ErrNotText):
		return fmt.Errorf("по ссылке не документ: %s", what)
	case errors.Is(err, weblink.ErrServer):
		return fmt.Errorf("сайт не ответил: %s — попробуйте еще раз", what)
	case errors.Is(err, weblink.ErrBadURL):
		return fmt.Errorf("это не ссылка http или https: %s", what)
	}
	return fmt.Errorf("не прочитан %s: %w", what, err)
}

// useAttachments wires reading of attached documents: файл из папок окна —
// через индекс, файл вне их — с диска, ссылка — weblink, найденное — тем же
// чтением, что у модели.
func (d *chatDesk) useAttachments(files *filesearch.Service, ext extraction, web weblink.Client, gd *googleDesk) {
	d.attach = func(ctx context.Context, ref string) (chat.Doc, error) {
		if link, ok := strings.CutPrefix(ref, "url:"); ok {
			return readLink(ctx, ext, web, gd, ref, link)
		}
		if p, ok := strings.CutPrefix(ref, "file:"); ok {
			if text, err := files.Read(p); err == nil {
				return chat.Doc{Ref: ref, Title: filepath.Base(p), Source: filepath.Dir(p), Text: text}, nil
			}
			return readDiskFile(ctx, ext, p)
		}
		return d.read(ctx, ref)
	}
}

func readLink(ctx context.Context, ext extraction, web weblink.Client, gd *googleDesk, ref, link string) (chat.Doc, error) {
	// Документ Google — через подключение (закрытые тоже); подключения нет —
	// как открытая страница.
	if gd != nil && google.FileID(link) != "" {
		d, err := gd.Read(ctx, link)
		switch {
		case err == nil:
			text := d.Text
			if d.Raw != nil {
				if text, err = extractBytes(ctx, ext, d.Raw, d.Ext); err != nil {
					return chat.Doc{}, err
				}
			}
			return chat.Doc{Ref: ref, Title: d.Name, Source: googleSource(gd), Text: text}, nil
		case !errors.Is(err, errNoGoogle):
			return chat.Doc{}, err
		}
	}
	p, err := web.Fetch(ctx, link)
	if err != nil {
		return chat.Doc{}, err
	}
	text := p.Text
	if p.Raw != nil {
		if text, err = extractBytes(ctx, ext, p.Raw, p.Ext); err != nil {
			return chat.Doc{}, err
		}
	}
	host := link
	if u, err := url.Parse(link); err == nil {
		host = u.Host
	}
	return chat.Doc{Ref: ref, Title: p.Title, Source: host, Text: text}, nil
}

// googleSource names the connection a Google document came through.
func googleSource(gd *googleDesk) string {
	for _, c := range gd.settings.snapshot().cfg.Connections {
		if c.Kind == desktopconfig.ConnGoogle && c.Enabled {
			return c.Name
		}
	}
	return "Google"
}

// readDiskFile reads a file the person picked outside the window's folders:
// текст как есть, docx — текстом, PDF и картинки — помощником распознавания.
func readDiskFile(ctx context.Context, ext extraction, path string) (chat.Doc, error) {
	doc := chat.Doc{Ref: "file:" + path, Title: filepath.Base(path), Source: filepath.Dir(path)}
	kind := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if kind == "pdf" || domain.FileKindOf(path) == domain.FileKindImage {
		if ext.helper == nil {
			return chat.Doc{}, errors.New("распознавание PDF и картинок работает только в собранном окне")
		}
	}
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return chat.Doc{}, err
	case !info.Mode().IsRegular():
		return chat.Doc{}, errors.New("это не файл")
	case info.Size() > maxDiskFile:
		return chat.Doc{}, errors.New("файл больше 20 МБ")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return chat.Doc{}, err
	}
	if doc.Text, err = extractBytes(ctx, ext, raw, kind); err != nil {
		return chat.Doc{}, err
	}
	return doc, nil
}

// extractBytes turns a file's bytes into text by its kind.
func extractBytes(ctx context.Context, ext extraction, raw []byte, kind string) (string, error) {
	switch {
	case kind == "pdf" || domain.FileKindOf("x."+kind) == domain.FileKindImage:
		if ext.helper == nil {
			return "", errors.New("распознавание PDF и картинок работает только в собранном окне")
		}
		text, err := ext.helper.Extract(ctx, bytes.NewReader(raw), kind)
		if err == nil && strings.TrimSpace(text) == "" {
			err = errors.New("текста не нашлось")
		}
		return text, err
	case kind == "docx":
		return localfiles.DocxText(bytes.NewReader(raw), int64(len(raw)))
	case !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0:
		return "", errors.New("это не текст: приложите docx, pdf, картинку или текстовый файл")
	}
	return string(raw), nil
}
