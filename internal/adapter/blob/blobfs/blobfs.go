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

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
)

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
	if err := blob.ValidKey(key); err != nil {
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
// ponytail: один замок на все хранилище, а не на ключ. Потолок — записи разных
// ключей ждут друг друга; на двух пользователях это миллисекунды. Путь
// апгрейда — замок на ключ в отдельном каталоге замков вне дерева данных.
func (s *Store) Put(ctx context.Context, key string, data []byte, ifMatch string) (string, error) {
	if err := blob.ValidKey(key); err != nil {
		return "", err
	}
	if len(data) > blob.MaxObjectSize {
		return "", fmt.Errorf("%w: %d bytes", blob.ErrTooLarge, len(data))
	}
	path := s.path(key)
	etag := etagOf(data)
	// Замок — файл рядом с корнем, а не внутри: внутри любое имя может
	// оказаться чьим-то ключом.
	err := filelock.WithContext(ctx, s.root, func() error {
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

// path maps a valid key under the root. ValidKey уже отказал `..`, абсолютным
// путям и обратной косой черте, поэтому Join не выходит из корня.
func (s *Store) path(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}

func etagOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// readLimited reads a file of at most blob.MaxObjectSize bytes.
func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
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
	return os.Rename(tmpName, path)
}
