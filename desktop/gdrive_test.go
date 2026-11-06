package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/google"
	"github.com/daniil/kb-engine/internal/usecase/chat"
)

const driveLink = "https://drive.google.com/drive/folders/F1?usp=sharing"

// Папка Диска в общей выдаче: находки самого Диска — записями со ссылкой;
// подключение выключено — источник назван с причиной, а не молчит.
func TestSearchSources_GDrive(t *testing.T) {
	s, _, _ := newServerSettings(t)
	gd := desktopconfig.Source{ID: "gd1", Kind: desktopconfig.KindGDrive, Name: "Тендеры", URL: driveLink, Conn: "google-1", Enabled: true}
	if err := s.updateConfig(func(c *desktopconfig.Config) {
		c.Sources = append(c.WithSources().Sources, gd)
		c.Connections = []desktopconfig.Connection{{ID: "google-1", Kind: desktopconfig.ConnGoogle, Name: "Google · p@example.com", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	var asked []string
	s.drive = func(_ context.Context, conn, folder, q string) ([]google.File, error) {
		asked = append(asked, conn+"/"+folder+"/"+q)
		return []google.File{{ID: "d1", Name: "ТЗ поставки", MimeType: "application/vnd.google-apps.document",
			Link: "https://docs.google.com/document/d/d1/edit", Modified: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)}}, nil
	}
	res := s.SearchSources(context.Background(), "поставки")
	if strings.Join(asked, ",") != "google-1/F1/поставки" {
		t.Fatalf("asked = %v", asked)
	}
	var hit *sourceHit
	for i := range res.Hits {
		if res.Hits[i].Source == "gd1" {
			hit = &res.Hits[i]
		}
	}
	if hit == nil {
		t.Fatalf("находок Диска нет: %+v", res)
	}
	var e struct{ Title, URL, Category string }
	_ = json.Unmarshal(hit.Entry, &e)
	if e.Title != "ТЗ поставки" || e.URL != "https://docs.google.com/document/d/d1/edit" || e.Category != "Google Диск" {
		t.Fatalf("entry = %s", hit.Entry)
	}
	// В чате находка Диска читается по ссылке (через подключение).
	if doc, ok := entryDoc(*hit, "Тендеры"); !ok || doc.Ref != "url:https://docs.google.com/document/d/d1/edit" || doc.SourceID != "gd1" {
		t.Fatalf("doc = %+v", doc)
	}

	// Подключение выключено — причина в строке источника.
	_ = s.updateConfig(func(c *desktopconfig.Config) { c.Connections[0].Enabled = false })
	res = s.SearchSources(context.Background(), "поставки")
	if r := findRow(res.Sources, "gd1"); r == nil || !strings.Contains(r.Error, "Google") {
		t.Fatalf("row = %+v", r)
	}
	// Отказ Диска — словами, без падения выдачи.
	_ = s.updateConfig(func(c *desktopconfig.Config) { c.Connections[0].Enabled = true })
	s.drive = func(context.Context, string, string, string) ([]google.File, error) {
		return nil, errors.New("у подключенного аккаунта нет доступа к документу")
	}
	if r := findRow(s.SearchSources(context.Background(), "x").Sources, "gd1"); r == nil || !strings.Contains(r.Error, "нет доступа") {
		t.Fatalf("row = %+v", r)
	}
}

func findRow(rows []sourceFound, id string) *sourceFound {
	for i := range rows {
		if rows[i].ID == id {
			return &rows[i]
		}
	}
	return nil
}

// Модель читает только ссылки, пришедшие из поиска (находки Диска): ссылку,
// придуманную по подсказке из чужого документа, окно не открывает — иначе
// данные чата ушли бы в адрес чужого сайта или закрытый документ прочелся бы
// чужим токеном (ревью #467).
func TestChatRead_URLOnlyFromSearch(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	var opened []string
	d.attach = func(_ context.Context, ref string) (chat.Doc, error) {
		opened = append(opened, ref)
		return chat.Doc{Ref: ref, Text: "текст"}, nil
	}
	d.useSources(nil)
	d.remember([]chat.Doc{{Ref: "url:https://docs.google.com/document/d/d1/edit", Title: "ТЗ"}})
	if _, err := d.read(context.Background(), "url:https://docs.google.com/document/d/d1/edit"); err != nil {
		t.Fatalf("находка из поиска не прочлась: %v", err)
	}
	for _, bad := range []string{"url:https://attacker.example/?d=секрет", "url:https://docs.google.com/document/d/private/edit"} {
		if _, err := d.read(context.Background(), bad); err == nil {
			t.Errorf("модель открыла %q", bad)
		}
	}
	if len(opened) != 1 {
		t.Fatalf("открыто: %v", opened)
	}
}

// Находка Диска читается подключением своего источника, а не первым
// включенным Google: у второго аккаунта документ, которого нет у первого
// (ревью #467).
func TestChatRead_DriveHitThroughItsConnection(t *testing.T) {
	d, _, _, _ := newTestChatDesk(t)
	if err := d.settings.updateConfig(func(c *desktopconfig.Config) {
		c.Sources = append(c.WithSources().Sources, desktopconfig.Source{ID: "gd1", Kind: desktopconfig.KindGDrive, URL: driveLink, Conn: "google-2", Enabled: true})
		c.Connections = []desktopconfig.Connection{
			{ID: "google-1", Kind: desktopconfig.ConnGoogle, Name: "Google · a@example.com", Enabled: true},
			{ID: "google-2", Kind: desktopconfig.ConnGoogle, Name: "Google · b@example.com", Enabled: true},
		}
	}); err != nil {
		t.Fatal(err)
	}
	var via []string
	d.attach = func(_ context.Context, ref string) (chat.Doc, error) {
		via = append(via, "first")
		return chat.Doc{Ref: ref}, nil
	}
	d.driveRead = func(_ context.Context, conn, ref string) (chat.Doc, error) {
		via = append(via, conn)
		return chat.Doc{Ref: ref, Text: "текст"}, nil
	}
	d.useSources(nil)
	ref := "url:https://docs.google.com/document/d/d1/edit"
	d.remember([]chat.Doc{{Ref: ref, Title: "ТЗ", SourceID: "gd1"}})
	if _, err := d.read(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if strings.Join(via, ",") != "google-2" {
		t.Fatalf("прочитано через %v, want google-2", via)
	}
}

// Запертая Связка (ждет пароля) не держит поиск Диска дольше срока поиска:
// как у kb-серверов, чтение секрета — с тем же сроком (ревью #467).
func TestGoogle_SearchDoesNotWaitForLockedKeychain(t *testing.T) {
	g, _, s := newTestGoogle(t)
	if err := s.updateConfig(func(c *desktopconfig.Config) {
		c.Connections = []desktopconfig.Connection{{ID: "google-1", Kind: desktopconfig.ConnGoogle, Name: "Google · a@example.com", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s.tokens = hangingTokens{release}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := g.Search(ctx, "google-1", "F1", "поставки")
	if d := time.Since(start); err == nil || d > 2*time.Second {
		t.Fatalf("Search = %v за %v", err, d)
	}
}

// Новая или измененная папка Диска сохраняется только с существующим
// подключением Google; папка, чье подключение удалили позже, не мешает
// сохранить соседей (ревью #467).
func TestSetSources_DriveNeedsExistingGoogle(t *testing.T) {
	s, _, _ := newServerSettings(t)
	if err := s.updateConfig(func(c *desktopconfig.Config) {
		c.Connections = []desktopconfig.Connection{{ID: "google-1", Kind: desktopconfig.ConnGoogle, Name: "Google · a@example.com", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	base := []desktopconfig.Source{src("folder-1", desktopconfig.KindFolder, "~/notes", true)}
	with := func(conn string) []desktopconfig.Source {
		return append(slices.Clone(base), desktopconfig.Source{ID: "gd1", Kind: desktopconfig.KindGDrive, URL: driveLink, Conn: conn, Enabled: true})
	}
	if err := s.SetSources(with("google-nope"), nil, nil); err == nil {
		t.Fatal("сохранена папка Диска с несуществующим подключением")
	}
	if err := s.SetSources(with("google-1"), nil, nil); err != nil {
		t.Fatalf("папка Диска с подключением: %v", err)
	}
	if err := s.updateConfig(func(c *desktopconfig.Config) { c.Connections = nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSources(s.snapshot().cfg.Sources, nil, nil); err != nil {
		t.Fatalf("старая папка без подключения не дает сохранить: %v", err)
	}
}
