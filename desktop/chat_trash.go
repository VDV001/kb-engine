package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Корзина чатов (ревью #662): удаление с телефона или в окне не стирает
// историю сразу — чат лежит в chats/.trash неделю, окно его возвращает.

// trashKeep — сколько удаленный чат лежит в корзине.
const trashKeep = 7 * 24 * time.Hour

func (d *chatDesk) trashDir() string { return filepath.Join(d.dir, ".trash") }

// toTrash moves chat file p to the trash; время удаления — mtime файла.
// Под d.mu: ход и переименование не пишут чат поверх удаления.
func (d *chatDesk) toTrash(id, p string) error {
	if err := os.MkdirAll(d.trashDir(), 0o700); err != nil {
		return err
	}
	dst := filepath.Join(d.trashDir(), id+".json")
	if err := os.Rename(p, dst); err != nil {
		return err
	}
	now := d.now()
	return os.Chtimes(dst, now, now)
}

// Trash lists deleted chats, свежие сверху; старше недели — стирает.
func (d *chatDesk) Trash() ([]chatSummary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	files, err := filepath.Glob(filepath.Join(d.trashDir(), "*.json"))
	if err != nil {
		return nil, err
	}
	out := []chatSummary{}
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		if _, err := d.trashed(id); err != nil {
			continue // испорченный или просроченный не прячет остальные
		}
		rec, err := d.loadFrom(f, id)
		if err != nil {
			continue
		}
		out = append(out, chatSummary{ID: rec.ID, Title: rec.Title, Updated: rec.Updated})
	}
	slices.SortFunc(out, func(a, b chatSummary) int { return b.Updated.Compare(a.Updated) })
	return out, nil
}

// Restore returns a deleted chat to the list. Связь с телефоном не
// возвращается: телефон чат уже забыл, вернутый живет только в окне.
func (d *chatDesk) Restore(id string) error {
	p, err := d.path(id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	src, err := d.trashed(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err == nil {
		return errors.New("такой чат уже есть в списке")
	}
	return os.Rename(src, p)
}

// trashed is the trash file of chat id; просроченный стирается и не найден.
func (d *chatDesk) trashed(id string) (string, error) {
	if !chatIDRe.MatchString(id) {
		return "", errors.New("нет такого чата в корзине")
	}
	f := filepath.Join(d.trashDir(), id+".json")
	info, err := os.Stat(f)
	if errors.Is(err, fs.ErrNotExist) {
		return "", errors.New("нет такого чата в корзине")
	}
	if err != nil {
		return "", err
	}
	if d.now().Sub(info.ModTime()) > trashKeep {
		_ = os.Remove(f)
		return "", errors.New("чат пролежал в корзине больше недели и стерт")
	}
	return f, nil
}
