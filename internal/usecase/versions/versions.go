// Package versions собирает файлы в семейства — итерации одного документа —
// и подписывает каждую версию: что это за версия и что нового относительно
// предыдущей. Чистое ядро без диска: файлы приходят с хешем и текстом.
package versions

import "time"

// File — один файл на входе: хеш и текст считает вызывающий.
type File struct {
	Path string
	Size int64
	Mod  time.Time
	Hash string // SHA-256; пусто — не считался
	Text string // пусто — текст недоступен
}

// Version — версия в семействе с подписью «что нового».
type Version struct {
	File
	Dups []string // пути байт-в-байт копий
	Note string
}

// Family — итерации одного документа лентой по времени.
type Family struct {
	Name     string
	Versions []Version
}

// Group собирает файлы в семейства.
func Group(files []File) []Family { return nil }
