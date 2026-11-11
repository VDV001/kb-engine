package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// Экран «Хранилище» (#543): сколько места занимает то, что окно копит на диске, и
// очистка по одному пункту. Размеры считаются по Stat, содержимое не читается;
// каждый пункт называет, что пропадет. Общей кнопки «очистить все» нет: история
// чатов и вход в сайты стираются только каждый своим решением.

// storageItem is one row of the screen.
type storageItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Loses  string `json:"loses"`  // что пропадет при очистке, словами окна
	Danger bool   `json:"danger"` // очистка необратима для человека, а не для кэша
	Bytes  int64  `json:"bytes"`
	Files  int    `json:"files"`
}

type storageSpec struct {
	storageItem
	path string
	dir  bool // папка: очищается содержимое, сама папка остается
}

type storageDesk struct{ specs []storageSpec }

// newStorageDesk wires the four items of #543; cfgDir — папка настроек окна,
// profile — профиль браузера DVK (отдельный от повседневного).
func newStorageDesk(cfgDir, profile string) *storageDesk {
	return &storageDesk{specs: []storageSpec{
		{storageItem{ID: "chats", Name: "История чатов", Loses: "Все разговоры с моделью. Настройки чата и ключ останутся."}, filepath.Join(cfgDir, "chats"), true},
		{storageItem{ID: "prices", Name: "Кэш цен моделей", Loses: "Ничего своего: список цен скачается заново."}, filepath.Join(cfgDir, "prices.json"), false},
		{storageItem{ID: "usage", Name: "Журнал вызовов модели", Loses: "Статистика расхода: Лента, Карта и Деньги начнутся с нуля."}, filepath.Join(cfgDir, "usage.jsonl"), false},
		{storageItem{ID: "profile", Name: "Профиль браузера", Danger: true, Loses: "Входы в сайты, cookies и история страниц «Браузера»: везде придется входить заново."}, profile, true},
	}}
}

// Items returns the rows with their current sizes; нет папки или файла — ноль.
func (d *storageDesk) Items() []storageItem {
	out := make([]storageItem, 0, len(d.specs))
	for _, s := range d.specs {
		it := s.storageItem
		it.Bytes, it.Files = measure(s.path)
		out = append(out, it)
	}
	return out
}

// measure sums regular files under path by Stat; ссылки не разворачиваются.
func measure(path string) (bytes int64, files int) {
	_ = filepath.WalkDir(path, func(_ string, e fs.DirEntry, err error) error {
		if err != nil || !e.Type().IsRegular() {
			return nil // нечитаемое место не считаем, а не роняем экран
		}
		if info, err := e.Info(); err == nil {
			bytes += info.Size()
			files++
		}
		return nil
	})
	return bytes, files
}

// Clear wipes one item by its id; чужие id отказ, соседние пункты не задеты.
func (d *storageDesk) Clear(id string) error {
	i := slices.IndexFunc(d.specs, func(s storageSpec) bool { return s.ID == id })
	if i < 0 {
		return fmt.Errorf("хранилище: нет пункта %q", id)
	}
	s := d.specs[i]
	if s.ID == "profile" {
		// Chrome держит SingletonLock в профиле, пока запущен; стирать под ним нельзя.
		if _, err := os.Lstat(filepath.Join(s.path, "SingletonLock")); err == nil {
			return errors.New("браузер запущен: сначала выключите его во вкладке «Браузер»")
		}
	}
	if !s.dir {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("хранилище: %w", err)
		}
		return nil
	}
	entries, err := os.ReadDir(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("хранилище: %w", err)
	}
	var errs []error
	for _, e := range entries {
		errs = append(errs, os.RemoveAll(filepath.Join(s.path, e.Name())))
	}
	return errors.Join(errs...)
}

// browserProfileDir is where `kbengine browser serve` keeps its profile when the
// window starts it without --profile (cmd/kbengine/browser.go, флаг profile).
// ponytail: путь продублирован из CLI; общий пакет пути — когда у профиля появится третий читатель.
func browserProfileDir() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "kb-browser", "profile")
}
