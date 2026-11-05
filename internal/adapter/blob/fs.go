package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"
)

// fsReadTimeout bounds one Open: fs.FS не несет ctx, а запрос не должен
// висеть на зависшем хранилище бесконечно.
const fsReadTimeout = 30 * time.Second

// FS exposes the objects under prefix as a read-only fs.FS: маршрут /kb/
// отдает заметки пользователя из хранилища тем же кодом, что с диска.
// Открываются только файлы; каталогов и их листинга нет — маршрут называет
// файл по полю file записи и ничего не перечисляет.
//
// ctx — время жизни сервера; каждое чтение ограничено fsReadTimeout.
func FS(ctx context.Context, s Store, prefix string) (fs.FS, error) {
	if prefix != "" {
		trimmed, ok := strings.CutSuffix(prefix, "/")
		if !ok {
			return nil, fmt.Errorf("blob: FS prefix %q must end with /", prefix)
		}
		if err := ValidKey(trimmed); err != nil {
			return nil, fmt.Errorf("blob: FS prefix: %w", err)
		}
	}
	return storeFS{ctx: ctx, store: s, prefix: prefix}, nil
}

type storeFS struct {
	ctx    context.Context
	store  Store
	prefix string
}

func (f storeFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) || name == "." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	ctx, cancel := context.WithTimeout(f.ctx, fsReadTimeout)
	defer cancel()
	o, err := f.store.Get(ctx, f.prefix+name)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	case errors.Is(err, ErrInvalidKey):
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	case err != nil:
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return &memFile{Reader: bytes.NewReader(o.Data), name: path.Base(name)}, nil
}

// memFile is an object held in memory: Read и Seek от bytes.Reader, этого
// хватает http.ServeContent.
type memFile struct {
	*bytes.Reader
	name string
}

func (m *memFile) Stat() (fs.FileInfo, error) { return fileInfo{name: m.name, size: m.Size()}, nil }
func (*memFile) Close() error                 { return nil }

// fileInfo. Времени правки у объекта здесь нет: нулевое время ServeContent
// понимает как «не ставить Last-Modified».
type fileInfo struct {
	name string
	size int64
}

func (i fileInfo) Name() string     { return i.name }
func (i fileInfo) Size() int64      { return i.size }
func (fileInfo) Mode() fs.FileMode  { return 0o444 }
func (fileInfo) ModTime() time.Time { return time.Time{} }
func (fileInfo) IsDir() bool        { return false }
func (fileInfo) Sys() any           { return nil }
