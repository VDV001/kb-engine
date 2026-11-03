package localfiles

import (
	"context"
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
// is extracted, and which Extracted does not have yet. open is the path to hand
// to the extractor: с раскрытыми симлинками корня, найденный самим обходом.
func (s *Source) Pending(ctx context.Context, fn func(doc domain.FileDoc, open string) error) error {
	if s.extracted == nil {
		return nil
	}
	return s.walk(ctx, func(shown, open string, info os.FileInfo, imgs int) error {
		name := info.Name()
		if !extractable(name) || !s.allowedExtract(name, imgs) {
			return nil
		}
		if _, ok := s.extracted.Text(shown, info.Size(), info.ModTime()); ok {
			return nil
		}
		doc, err := domain.NewFileDoc(shown, info.Size(), info.ModTime())
		if err != nil {
			return err
		}
		return fn(doc, open)
	})
}

// allowedExtract applies the folder cap: картинки из папки кадров не берутся.
func (s *Source) allowedExtract(name string, imgs int) bool {
	return !isImage(name) || imgs <= MaxImagesPerDir
}

// extractedText returns the stored text of a file the walk would index, and
// false if there is none yet.
func (s *Source) extractedText(shown string, info os.FileInfo) (string, bool) {
	if s.extracted == nil {
		return "", false
	}
	return s.extracted.Text(shown, info.Size(), info.ModTime())
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
