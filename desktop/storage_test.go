package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// put writes size bytes to dir/rel and returns the path.
func put(t *testing.T, dir, rel string, size int) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func newStorage(t *testing.T) (*storageDesk, string, string) {
	t.Helper()
	cfg, profile := t.TempDir(), filepath.Join(t.TempDir(), "profile")
	return newStorageDesk(cfg, profile), cfg, profile
}

func itemByID(t *testing.T, d *storageDesk, id string) storageItem {
	t.Helper()
	for _, it := range d.Items() {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("нет пункта %q", id)
	return storageItem{}
}

// Размеры считаются по Stat, содержимое не читается: файл без прав на чтение
// все равно учтен.
func TestStorageDesk_sizes(t *testing.T) {
	d, cfg, profile := newStorage(t)
	put(t, cfg, "chats/a.json", 10)
	put(t, cfg, "chats/b.json", 5)
	put(t, cfg, "prices.json", 7)
	unread := put(t, cfg, "usage.jsonl", 3)
	if err := os.Chmod(unread, 0); err != nil {
		t.Fatal(err)
	}
	put(t, profile, "Default/Cookies", 100)
	for _, c := range []struct {
		id    string
		bytes int64
		files int
	}{{"chats", 15, 2}, {"prices", 7, 1}, {"usage", 3, 1}, {"profile", 100, 1}} {
		it := itemByID(t, d, c.id)
		if it.Bytes != c.bytes || it.Files != c.files {
			t.Errorf("%s: %d байт в %d файлах, нужно %d в %d", c.id, it.Bytes, it.Files, c.bytes, c.files)
		}
	}
}

// Нет папки или файла — это ноль, а не ошибка: свежая установка.
func TestStorageDesk_missingIsZero(t *testing.T) {
	d, _, _ := newStorage(t)
	for _, it := range d.Items() {
		if it.Bytes != 0 || it.Files != 0 {
			t.Errorf("%s: %+v", it.ID, it)
		}
	}
}

// Каждая очистка называет, что пропадет; опасна только очистка профиля.
func TestStorageDesk_everyItemSaysWhatIsLost(t *testing.T) {
	d, _, _ := newStorage(t)
	for _, it := range d.Items() {
		if strings.TrimSpace(it.Loses) == "" || it.Name == "" {
			t.Errorf("%s: нет названия или того, что пропадет", it.ID)
		}
		if it.Danger != (it.ID == "profile") {
			t.Errorf("%s: Danger=%v", it.ID, it.Danger)
		}
	}
}

// Очистка одного пункта не задевает соседей; папка остается.
func TestStorageDesk_clearTouchesOnlyItsItem(t *testing.T) {
	d, cfg, _ := newStorage(t)
	put(t, cfg, "chats/a.json", 10)
	put(t, cfg, "prices.json", 7)
	put(t, cfg, "usage.jsonl", 3)
	if err := d.Clear("chats"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "chats")); err != nil {
		t.Errorf("папка чатов должна остаться: %v", err)
	}
	if it := itemByID(t, d, "chats"); it.Bytes != 0 {
		t.Errorf("чаты не очищены: %+v", it)
	}
	if itemByID(t, d, "prices").Bytes != 7 || itemByID(t, d, "usage").Bytes != 3 {
		t.Error("очистка чатов задела соседей")
	}
	if err := d.Clear("prices"); err != nil || itemByID(t, d, "prices").Bytes != 0 {
		t.Errorf("prices: %v", err)
	}
	if err := d.Clear("usage"); err != nil || itemByID(t, d, "usage").Bytes != 0 {
		t.Errorf("usage: %v", err)
	}
}

func TestStorageDesk_clearUnknownRefused(t *testing.T) {
	d, cfg, _ := newStorage(t)
	put(t, cfg, "config.json", 9)
	for _, id := range []string{"", "nope", "config", "../config.json"} {
		if err := d.Clear(id); err == nil {
			t.Errorf("Clear(%q) не отказал", id)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg, "config.json")); err != nil {
		t.Errorf("настройки не должны быть тронуты: %v", err)
	}
}

// Профиль занят запущенным браузером (Chrome кладет SingletonLock): отказ, ничего
// не стерто. Без замка — стирается целиком.
func TestStorageDesk_clearProfileRefusedWhileBrowserRuns(t *testing.T) {
	d, _, profile := newStorage(t)
	put(t, profile, "Default/Cookies", 100)
	if err := os.Symlink("host-1234", filepath.Join(profile, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	err := d.Clear("profile")
	if err == nil || !strings.Contains(err.Error(), "запущен") {
		t.Fatalf("нужен отказ «браузер запущен», получили %v", err)
	}
	if itemByID(t, d, "profile").Bytes != 100 {
		t.Fatal("профиль тронут при отказе")
	}
	if err := os.Remove(filepath.Join(profile, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	if err := d.Clear("profile"); err != nil {
		t.Fatal(err)
	}
	if it := itemByID(t, d, "profile"); it.Bytes != 0 {
		t.Errorf("профиль не очищен: %+v", it)
	}
}
