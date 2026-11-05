// Package blob is the storage port for the server's data: каталог базы,
// заметки и финансы каждого пользователя лежат объектами по ключу, на диске
// Мака или в бакете Object Storage. Записи вслепую в порте нет: создать можно
// только отсутствующий ключ, обновить — только ту версию, что писатель прочитал.
// Два писателя с одной версией не затирают друг друга: второй получает
// ErrConflict, перечитывает и решает сам.
package blob

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrNotFound — ключа нет.
	ErrNotFound = errors.New("blob: key not found")
	// ErrConflict — хранимая версия не та, от которой писал писатель: ее
	// изменил кто-то другой, ключ уже создан или удален.
	ErrConflict = errors.New("blob: stored version changed")
	// ErrInvalidKey — ключ не прошел ValidKey.
	ErrInvalidKey = errors.New("blob: invalid key")
	// ErrTooLarge — объект больше MaxObjectSize.
	ErrTooLarge = errors.New("blob: object too large")
)

// MaxObjectSize bounds one object: Get и Put держат объект в памяти целиком.
// Самый большой файл базы на 05.11.2026 — search-index.json, 23 МБ.
const MaxObjectSize = 64 << 20

// maxKeyLen is the S3 limit on a key, in bytes.
const maxKeyLen = 1024

// Object is a stored value with its version.
type Object struct {
	Data []byte
	// ETag identifies this version. Он непрозрачен: сравнивается только на
	// равенство и передается в Put как есть.
	ETag string
}

// Store keeps objects under slash-separated keys.
type Store interface {
	// Get returns the object, or ErrNotFound.
	Get(ctx context.Context, key string) (Object, error)
	// Put writes data and returns the new ETag. С пустым ifMatch ключа еще не
	// должно быть, иначе ifMatch — ETag версии, которую писатель прочитал. Если
	// условие не выполнено, данные не меняются и возвращается ErrConflict.
	Put(ctx context.Context, key string, data []byte, ifMatch string) (string, error)
}

// ValidKey refuses keys that could name something outside their own place:
// абсолютный путь, сегменты `.` и `..`, пустой сегмент, обратная косая черта
// и управляющие символы. На диске такой ключ вышел бы из корня, в бакете — из
// каталога своего пользователя.
func ValidKey(key string) error {
	switch {
	case key == "":
		return fmt.Errorf("%w: empty", ErrInvalidKey)
	case len(key) > maxKeyLen:
		return fmt.Errorf("%w: longer than %d bytes", ErrInvalidKey, maxKeyLen)
	case !utf8.ValidString(key):
		return fmt.Errorf("%w: not UTF-8", ErrInvalidKey)
	case strings.ContainsFunc(key, func(r rune) bool { return r == '\\' || unicode.IsControl(r) }):
		return fmt.Errorf("%w: %q has a backslash or a control character", ErrInvalidKey, key)
	}
	for seg := range strings.SplitSeq(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: %q has an empty, . or .. segment", ErrInvalidKey, key)
		}
	}
	return nil
}
