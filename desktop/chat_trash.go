package main

import (
	"errors"
	"io/fs"
	"log"
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
// Под d.fmu: запись чата не идет поверх переноса.
func (d *chatDesk) toTrash(id, p string) error {
	if err := os.MkdirAll(d.trashDir(), 0o700); err != nil {
		return err
	}
	dst := filepath.Join(d.trashDir(), id+".json")
	if err := os.Rename(p, dst); err != nil {
		return err
	}
	now := d.now()
	// ponytail: не вышло поставить время — чат уже в корзине, удаление
	// состоялось; срок тогда считается от последней записи чата и может
	// выйти раньше недели. Путь: время удаления в имени файла.
	if err := os.Chtimes(dst, now, now); err != nil {
		log.Printf("chats: время удаления %s: %v", id, err)
	}
	return nil
}

// purgeTrash erases chats older than trashKeep: зовется списком чатов, чтобы
// неделя держалась и без показа корзины (ревью #665).
func (d *chatDesk) purgeTrash() {
	d.fmu.Lock()
	defer d.fmu.Unlock()
	files, _ := filepath.Glob(filepath.Join(d.trashDir(), "*.json"))
	for _, f := range files {
		_, _ = d.trashed(strings.TrimSuffix(filepath.Base(f), ".json"))
	}
}

// Trash lists deleted chats, свежие сверху; старше недели — стирает.
func (d *chatDesk) Trash() ([]chatSummary, error) {
	d.fmu.Lock()
	defer d.fmu.Unlock()
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
		out = append(out, chatSummary{ID: id, Title: rec.Title, Updated: rec.Updated}) // id — имя файла: по нему вернут
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
	d.fmu.Lock()
	defer d.fmu.Unlock()
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
