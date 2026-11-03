package domain

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ErrInvalidFilePath is returned when a file path is not absolute and clean.
var ErrInvalidFilePath = errors.New("invalid file path")

// ErrInvalidFileSize is returned when a file size is negative.
var ErrInvalidFileSize = errors.New("invalid file size")

// FileKind is the coarse type of a file on disk, the one a person filters by:
// a document, code, an image, or anything else.
type FileKind struct {
	value string
}

// The canonical file kinds.
var (
	FileKindDoc   = FileKind{"doc"}
	FileKindCode  = FileKind{"code"}
	FileKindImage = FileKind{"image"}
	FileKindOther = FileKind{"other"}
)

// kindByExt — расширение решает тип, а не содержимое: тип нужен фильтру в
// выдаче, и человек думает о файле именно так — «это картинка», а не «это
// байты PNG». Неизвестное расширение честно уходит в «прочее».
var kindByExt = map[string]FileKind{
	".md": FileKindDoc, ".txt": FileKindDoc, ".html": FileKindDoc, ".pdf": FileKindDoc,
	".docx": FileKindDoc, ".rtf": FileKindDoc, ".csv": FileKindDoc,
	".go": FileKindCode, ".py": FileKindCode, ".ts": FileKindCode, ".tsx": FileKindCode,
	".js": FileKindCode, ".jsx": FileKindCode, ".sh": FileKindCode, ".json": FileKindCode,
	".yml": FileKindCode, ".yaml": FileKindCode, ".toml": FileKindCode, ".sql": FileKindCode,
	".css": FileKindCode, ".nix": FileKindCode, ".swift": FileKindCode, ".rs": FileKindCode,
	".png": FileKindImage, ".jpg": FileKindImage, ".jpeg": FileKindImage, ".gif": FileKindImage,
	".webp": FileKindImage, ".heic": FileKindImage,
}

// FileKindOf classifies a path by its extension, case-insensitively.
func FileKindOf(path string) FileKind {
	if k, ok := kindByExt[strings.ToLower(filepath.Ext(path))]; ok {
		return k
	}
	return FileKindOther
}

// String returns the canonical kind value.
func (k FileKind) String() string { return k.value }

// FileDoc is a value object for one file seen on disk: where it is, how big it
// is and when it last changed. It carries no content — reading is the
// adapter's job, and the index keeps only what it derived from the text.
type FileDoc struct {
	path    string
	size    int64
	modTime time.Time
	kind    FileKind
}

// NewFileDoc validates the path and size and returns a FileDoc.
//
// Путь обязан быть абсолютным и очищенным: по нему файл открывают из выдачи и
// по нему же сверяют, тот ли это файл при пересборке. Два написания одного
// пути («/a/../a/x») дали бы две записи об одном файле.
func NewFileDoc(path string, size int64, modTime time.Time) (FileDoc, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return FileDoc{}, fmt.Errorf("%w: %q", ErrInvalidFilePath, path)
	}
	if size < 0 {
		return FileDoc{}, fmt.Errorf("%w: %d", ErrInvalidFileSize, size)
	}
	return FileDoc{path: path, size: size, modTime: modTime, kind: FileKindOf(path)}, nil
}

// Path returns the absolute path.
func (d FileDoc) Path() string { return d.path }

// Name returns the base name of the file.
func (d FileDoc) Name() string { return filepath.Base(d.path) }

// Size returns the size in bytes.
func (d FileDoc) Size() int64 { return d.size }

// ModTime returns the last modification time.
func (d FileDoc) ModTime() time.Time { return d.modTime }

// Kind returns the coarse file type.
func (d FileDoc) Kind() FileKind { return d.kind }
