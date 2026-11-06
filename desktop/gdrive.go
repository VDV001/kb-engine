package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/google"
)

// Папка Google Диска как источник (решение владельца 06.11): ищет сам Диск
// (fullText) через подключение Google; файлы на Мак не скачиваются, находка —
// запись со ссылкой, чат читает ее через то же подключение.

// driveFunc searches folder of Google connection conn.
type driveFunc func(ctx context.Context, conn, folder, q string) ([]google.File, error)

var driveKind = map[string]string{
	"application/vnd.google-apps.document":     "документ Google",
	"application/vnd.google-apps.spreadsheet":  "таблица Google",
	"application/vnd.google-apps.presentation": "презентация Google",
	"application/pdf":                          "PDF",
}

// searchDrive asks Google Drive for q in the source's folder.
func (f *folderSettings) searchDrive(ctx context.Context, s desktopconfig.Source, cfg desktopconfig.Config, q string) ([]candidate, string) {
	conn, ok := cfg.Connection(s.Conn)
	switch {
	case !ok:
		return nil, "нет подключения Google — подключите в «Подключениях»"
	case !conn.Enabled:
		return nil, "подключение «" + conn.Name + "» выключено"
	case f.drive == nil:
		return nil, "Google Диск ищется в окне приложения"
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	files, err := f.drive(ctx, s.Conn, desktopconfig.DriveFolderID(s.URL), q)
	if err != nil {
		return nil, err.Error()
	}
	out := make([]candidate, 0, len(files))
	for _, file := range files {
		kind := driveKind[file.MimeType]
		if kind == "" {
			kind = "файл"
		}
		entry := map[string]any{"title": file.Name, "url": file.Link, "category": "Google Диск",
			"description": kind + " · изменен " + file.Modified.Format("02.01.2006"), "date": file.Modified.Format("2006-01-02")}
		out = append(out, candidate{key: "gdrive:" + file.ID, encode: func() (json.RawMessage, error) { return json.Marshal(entry) }})
	}
	return out, ""
}

// Search finds q in a folder through Google connection conn.
func (g *googleDesk) Search(ctx context.Context, conn, folder, q string) ([]google.File, error) {
	cfg := g.settings.snapshot().cfg
	if !slices.ContainsFunc(cfg.Connections, func(c desktopconfig.Connection) bool { return c.ID == conn && c.Enabled }) {
		return nil, errNoGoogle
	}
	c, at, err := g.token(ctx, conn)
	if err != nil {
		return nil, err
	}
	files, err := c.Search(ctx, at, folder, q)
	if err != nil {
		return nil, googleErr(err)
	}
	return files, nil
}

// driveConn is the Google connection of Drive folder source id; "" — не папка Диска.
func (f *folderSettings) driveConn(id string) string {
	for _, s := range f.snapshot().cfg.WithSources().Sources {
		if s.ID == id && s.Kind == desktopconfig.KindGDrive {
			return s.Conn
		}
	}
	return ""
}

// checkDriveConns refuses a new or changed Drive folder whose connection is
// not a Google connection of the window: страница прислала бы любой id.
// Папка, чье подключение удалили позже, остается («нет подключения» в строке)
// и не мешает сохранить соседей.
func checkDriveConns(prev, next desktopconfig.Config) error {
	old := map[string]desktopconfig.Source{}
	for _, s := range prev.WithSources().Sources {
		old[s.ID] = s
	}
	for _, s := range next.Sources {
		if s.Kind != desktopconfig.KindGDrive {
			continue
		}
		if o, ok := old[s.ID]; ok && o.Kind == s.Kind && o.Conn == s.Conn {
			continue
		}
		if c, ok := next.Connection(s.Conn); !ok || c.Kind != desktopconfig.ConnGoogle {
			return fmt.Errorf("%w: %s: нет такого подключения Google — выберите его в списке", desktopconfig.ErrSourceTarget, s.ID)
		}
	}
	return nil
}

// driveRow is the state of a Drive folder in the sources list: подключение
// есть и включено — ищется; нет — названо, что сделать.
func (f *folderSettings) driveRow(r *sourceRow, cfg desktopconfig.Config) {
	conn, ok := cfg.Connection(r.Conn)
	switch {
	case !ok:
		r.State, r.Note = stateMissing, "нет подключения Google — подключите в «Подключениях»"
	case !conn.Enabled:
		r.State, r.Note = stateMissing, "подключение «"+conn.Name+"» выключено"
	default:
		r.State, r.Note = stateOK, "ищет Google Диск через «"+conn.Name+"»"
	}
}
