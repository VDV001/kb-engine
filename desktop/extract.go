package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/extractcache"
	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/domain"
)

// extractBatch — после стольких распознанных файлов индекс пересобирается:
// первый проход по тысяче сканов идет десятки минут, и найденное должно
// появляться в поиске по ходу, а не в конце.
const extractBatch = 50

// extractor is kb-extract: text of one PDF or image.
type extractor interface {
	Extract(ctx context.Context, path string) (string, error)
}

// extraction — распознавание PDF и картинок окном: кеш, куда кладется текст,
// и помощник kb-extract. Без помощника окно только читает готовое; нулевое
// значение — PDF и картинки не индексируются вовсе.
type extraction struct {
	cache  extractcache.Cache
	helper extractor
}

// option opens sources with the recognized text, when there is a cache.
func (e extraction) option() localfiles.Option {
	if e.cache.Dir == "" {
		return func(*localfiles.Source) {}
	}
	return localfiles.WithExtracted(e.cache)
}

var errSuperseded = errors.New("folders changed")

// extractPending recognizes, one by one in the background, the PDFs and images
// of src that have no text yet, and rebuilds the index every extractBatch files
// and at the end. Второй проход, пока идет первый, не начинается: пересборка
// по ходу его же и запустила бы.
//
// ponytail: упавшее распознавание кладет пустой текст, и файл не
// распознается снова, пока не изменится. Потолок: сбой на время (помощник
// убит, диск занят) оставляет файл без текста. Путь: класть ошибку с
// отметкой времени и повторять раз в сутки.
func (x *indexer) extractPending(ctx context.Context, src *localfiles.Source) {
	if x.ext.helper == nil || !x.extracting.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer x.extracting.Store(false)
		start := time.Now()
		n, err := x.extractPass(ctx, src)
		if err != nil && !errors.Is(err, errSuperseded) && ctx.Err() == nil {
			log.Printf("files: распознавание: %v", err)
		}
		if n == 0 {
			return
		}
		log.Printf("files: распознано %d за %s", n, time.Since(start).Round(time.Second))
		if n%extractBatch != 0 && x.currentSource() == src {
			x.run(src)
		}
	}()
}

// extractPass recognizes the pending files of src and returns how many it did.
func (x *indexer) extractPass(ctx context.Context, src *localfiles.Source) (int, error) {
	n := 0
	err := src.Pending(ctx, func(d domain.FileDoc, open string) error {
		if x.currentSource() != src {
			return errSuperseded // владелец выбрал другие папки: распознает следующий проход
		}
		if err := x.extractOne(ctx, d, open); err != nil {
			return err
		}
		if n++; n%extractBatch == 0 {
			x.run(src)
		}
		return nil
	})
	return n, err
}

// extractOne recognizes one file and stores its text, empty when it failed.
func (x *indexer) extractOne(ctx context.Context, d domain.FileDoc, open string) error {
	text, err := x.ext.helper.Extract(ctx, open)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		log.Printf("files: не распознан %s: %v", d.Name(), err)
		text = ""
	}
	return x.ext.cache.Put(d.Path(), d.Size(), d.ModTime(), text)
}
