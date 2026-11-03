package localfiles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// MaxImagesPerDir — в папке, где картинок больше, это кадры роликов или
// рендеры, а не сканы и скриншоты: ее картинки не распознаются и не
// индексируются (замер 03.11: 5880 из 7422 картинок лежали в 11 таких папках).
const MaxImagesPerDir = 100

// Extracted is where the text of PDFs and images comes from: распознает их
// помощник kb-extract в окне, источник только читает готовое.
type Extracted interface {
	Text(path string, size int64, mod time.Time) (string, bool)
	// Has answers without reading the text: обход спрашивает о каждом файле.
	Has(path string, size int64, mod time.Time) bool
}

// Option configures a Source.
type Option func(*Source)

// WithExtracted indexes PDFs and images whose text e already has. Без него PDF
// и картинки не индексируются и не читаются.
func WithExtracted(e Extracted) Option { return func(s *Source) { s.extracted = e } }

func isImage(name string) bool { return domain.FileKindOf(name) == domain.FileKindImage }

// extractable tells whether a file's text comes from Extracted.
func extractable(name string) bool {
	return isImage(name) || strings.EqualFold(filepath.Ext(name), ".pdf")
}

// Pending calls fn for every PDF and image the walk would index once its text
// is extracted, and which Extracted does not have yet. Файл для помощника
// открывает OpenExtract.
func (s *Source) Pending(ctx context.Context, fn func(doc domain.FileDoc) error) error {
	if s.extracted == nil {
		return nil
	}
	return s.walk(ctx, func(shown string, info os.FileInfo, imgs int) error {
		name := info.Name()
		if !extractable(name) || !s.allowedExtract(name, imgs) || s.extracted.Has(shown, info.Size(), info.ModTime()) {
			return nil
		}
		doc, err := domain.NewFileDoc(shown, info.Size(), info.ModTime())
		if err != nil {
			return err
		}
		return fn(doc)
	})
}

// OpenExtract opens a PDF or image for the extractor through the root, under
// the rules of the walk: корни, исключения, секреты, без симлинков. Info — от
// открытого файла: ключ кеша описывает ту версию, которую прочтет помощник
// (ревью #379: ключ брался из обхода, а файл читался позже).
func (s *Source) OpenExtract(path string) (*os.File, os.FileInfo, error) {
	i, rel, ok := s.locate(path)
	if !ok || s.extracted == nil || !extractable(rel) || s.throughSkipped(rel) || !s.indexable(filepath.Base(rel)) || !plainFile(s.dirs[i], rel) {
		return nil, nil, fmt.Errorf("%w: %q", ErrNotAllowed, path)
	}
	f, err := s.dirs[i].Open(rel)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// allowedExtract applies the folder cap: картинки из папки кадров не берутся.
func (s *Source) allowedExtract(name string, imgs int) bool {
	return !isImage(name) || imgs <= MaxImagesPerDir
}

// hasExtracted reports whether the text of this version of the file is ready.
func (s *Source) hasExtracted(shown string, info os.FileInfo) bool {
	return s.extracted != nil && s.extracted.Has(shown, info.Size(), info.ModTime())
}

// countImages returns how many images lie directly in dir.
func countImages(entries []os.DirEntry) int {
	n := 0
	for _, e := range entries {
		if !e.IsDir() && isImage(e.Name()) {
			n++
		}
	}
	return n
}
