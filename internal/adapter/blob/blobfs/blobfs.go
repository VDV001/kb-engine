// Package blobfs keeps blob.Store objects as files under one root: ключ
// `users/u1/kb/catalog.json` — файл `<root>/users/u1/kb/catalog.json`. Так
// сервер работает на Маке и в тестах тем же кодом, что и с бакетом.
package blobfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
)

// LockName is the store's lock file inside the root; ключом он быть не может.
const LockName = ".blobfs.lock"

// Store is a blob.Store on the local disk.
type Store struct {
	root string
}

var _ blob.Store = (*Store)(nil)

// New opens (and creates, mode 0700) the store rooted at root.
func New(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("blobfs: empty root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("blobfs: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("blobfs: %w", err)
	}
	return &Store{root: abs}, nil
}

// Get reads the object at key.
func (s *Store) Get(_ context.Context, key string) (blob.Object, error) {
	if err := validKey(key); err != nil {
		return blob.Object{}, err
	}
	data, err := readLimited(s.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return blob.Object{}, fmt.Errorf("%w: %s", blob.ErrNotFound, key)
	}
	if err != nil {
		return blob.Object{}, err
	}
	return blob.Object{Data: data, ETag: etagOf(data)}, nil
}

// Put writes data at key if the stored version is still ifMatch (or, with an
// empty ifMatch, if the key does not exist yet).
//
// ponytail: один замок на все хранилище, а не на ключ, и версия считается
// перечитыванием и SHA-256 текущего файла. Потолок — записи разных ключей ждут
// друг друга, а запись 23 МБ индекса держит замок на чтение и хэш 23 МБ; на
// двух пользователях это доли секунды. Путь апгрейда — замок на ключ и ETag в
// файле рядом с данными. В облаке этого кода нет: там blobs3.
//
// ponytail: на APFS ключи, различающиеся только регистром или нормализацией
// Unicode (й в NFC и NFD), — один файл, а в бакете два объекта. Потолок —
// такие ключи здесь конфликтуют или делят данные; ключи базы приходят из имен
// файлов той же APFS и уже уникальны в ней. Путь апгрейда — приводить ключ к
// NFC (golang.org/x/text) и отказывать в ключах, равных без учета регистра.
func (s *Store) Put(ctx context.Context, key string, data []byte, ifMatch string) (string, error) {
	if err := validKey(key); err != nil {
		return "", err
	}
	if len(data) > blob.MaxObjectSize {
		return "", fmt.Errorf("%w: %d bytes", blob.ErrTooLarge, len(data))
	}
	path := s.path(key)
	etag := etagOf(data)
	// Замок внутри корня: хранилищу нужен только свой каталог (смонтированный
	// том, родитель только для чтения). Его имя отказано как ключ.
	err := filelock.WithContext(ctx, filepath.Join(s.root, strings.TrimSuffix(LockName, filelock.Suffix)), func() error {
		if err := s.collides(path); err != nil {
			return fmt.Errorf("%w: %s: %w", blob.ErrInvalidKey, key, err)
		}
		cur, err := readLimited(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if ifMatch != "" {
				return fmt.Errorf("%w: %s is gone", blob.ErrConflict, key)
			}
		case err != nil:
			return err
		case ifMatch == "":
			return fmt.Errorf("%w: %s already exists", blob.ErrConflict, key)
		case etagOf(cur) != ifMatch:
			return fmt.Errorf("%w: %s", blob.ErrConflict, key)
		}
		return writeAtomic(path, data)
	})
	if err != nil {
		return "", err
	}
	return etag, nil
}

// validKey is blob.ValidKey plus the store's own lock name.
func validKey(key string) error {
	if err := blob.ValidKey(key); err != nil {
		return err
	}
	if key == LockName {
		return fmt.Errorf("%w: %q is the store's lock", blob.ErrInvalidKey, key)
	}
	return nil
}

// errCollides: в бакете «a» и «a/b» — независимые ключи, а на диске один из
// них был бы каталогом другого.
var errCollides = errors.New("на диске ключ пересекается с каталогом или файлом другого ключа")

// collides reports whether path is a directory or sits under a regular file.
func (s *Store) collides(path string) error {
	if info, err := os.Lstat(path); err == nil && info.IsDir() {
		return errCollides
	}
	for dir := filepath.Dir(path); dir != s.root && len(dir) > len(s.root); dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errCollides
		}
	}
	return nil
}

// path maps a valid key under the root. ValidKey уже отказал `..`, абсолютным
// путям и обратной косой черте, поэтому Join не выходит из корня.
func (s *Store) path(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}

func etagOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// readLimited reads a file of at most blob.MaxObjectSize bytes. Каталог на
// месте ключа и путь под обычным файлом — «объекта нет», как в бакете.
func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if errors.Is(err, syscall.ENOTDIR) {
		return nil, fmt.Errorf("%w: %w", fs.ErrNotExist, err)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil {
		return nil, err
	} else if info.IsDir() {
		return nil, fmt.Errorf("%w: %s is a directory", fs.ErrNotExist, path)
	}
	data, err := io.ReadAll(io.LimitReader(f, blob.MaxObjectSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > blob.MaxObjectSize {
		return nil, fmt.Errorf("%w: %s", blob.ErrTooLarge, path)
	}
	return data, nil
}

// writeAtomic replaces path with data through a temp file in the same
// directory: читатель видит либо старую версию, либо новую, но не половину.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".blob-*.tmp") // CreateTemp создает с 0600
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	// Sync до rename: иначе после сбоя питания на месте файла мог бы
	// оказаться пустой, а старой версии уже нет.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	// Sync каталога: без него после сбоя питания rename мог не дойти до
	// диска, и подтвержденная запись вернулась бы к старой версии.
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

func (s *Store) GetIfChanged(context.Context, string, string) (blob.Object, error) {
	return blob.Object{}, errors.New("not implemented")
}
