package main

import (
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/extractcache"
	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/domain"
)

// extractBatch — после стольких распознанных файлов индекс пересобирается:
// первый проход по тысяче сканов идет десятки минут, и найденное должно
// появляться в поиске по ходу, а не в конце.
const extractBatch = 50

// extractor is kb-extract: text of one PDF or image read from r, ext — its kind.
type extractor interface {
	Extract(ctx context.Context, r io.Reader, ext string) (string, error)
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
// по ходу его же и запустила бы. Если за время прохода выбраны другие папки,
// проход останавливается, ничего не пересобирая на старых, и следующий
// начинается по новым (ревью #379: выбор папок откатывался, а новые оставались
// нераспознанными).
//
// ponytail: упавшее распознавание кладет пустой текст, и файл не
// распознается снова, пока не изменится; таймаут не кладет ничего и
// повторяется. Потолок: файл, на котором помощник падает раз за разом от
// временной причины. Путь: класть ошибку с отметкой времени и повторять раз в сутки.
func (x *indexer) extractPending(ctx context.Context, src *localfiles.Source) {
	if x.ext.helper == nil || !x.extracting.CompareAndSwap(false, true) {
		return
	}
	go func() {
		start := time.Now()
		n, err := x.extractPass(ctx, src)
		if err != nil && !errors.Is(err, errSuperseded) && ctx.Err() == nil {
			log.Printf("files: распознавание: %v", err)
		}
		if n > 0 {
			log.Printf("files: распознано %d за %s", n, time.Since(start).Round(time.Second))
			if n%extractBatch != 0 {
				x.rerun(src)
			}
		}
		x.extracting.Store(false)
		// Папки сменились, пока шел проход: сборка новых уже могла закончиться
		// и не запустить свой проход (этот держал флаг).
		if cur := x.currentSource(); cur != src && ctx.Err() == nil {
			x.extractPending(ctx, cur)
		}
	}()
}

// extractPass recognizes the pending files of src and returns how many it did.
// Ждущие собираются заранее: окно показывает «распознано N из M».
func (x *indexer) extractPass(ctx context.Context, src *localfiles.Source) (int, error) {
	var docs []domain.FileDoc
	if err := src.Pending(ctx, func(d domain.FileDoc) error { docs = append(docs, d); return nil }); err != nil {
		return 0, err
	}
	if len(docs) == 0 {
		return 0, nil
	}
	defer x.progress.Store(nil)
	n := 0
	for i, d := range docs {
		x.progress.Store(&recognizeProgress{Done: i, Total: len(docs)})
		if !x.latest(src) {
			return n, errSuperseded
		}
		done, err := x.extractOne(ctx, src, d)
		if err != nil {
			return n, err
		}
		if !done {
			continue
		}
		if n++; n%extractBatch == 0 {
			x.rerun(src)
		}
	}
	return n, nil
}

// extractOne recognizes one file and stores its text, empty when it failed.
// done is false when nothing was stored: таймаут повторится на следующем проходе.
func (x *indexer) extractOne(ctx context.Context, src *localfiles.Source, d domain.FileDoc) (bool, error) {
	f, info, err := src.OpenExtract(d.Path())
	if err != nil {
		return false, nil // файл пропал или стал симлинком после обхода
	}
	defer func() { _ = f.Close() }() // только чтение
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(d.Path())), ".")
	text, err := x.ext.helper.Extract(ctx, f, ext)
	switch {
	case ctx.Err() != nil:
		return false, ctx.Err()
	case errors.Is(err, extractcache.ErrTimeout):
		log.Printf("files: не успел распознать %s: %v", d.Name(), err)
		return false, nil
	case err != nil:
		log.Printf("files: не распознан %s: %v", d.Name(), err)
		text = ""
	}
	// Ключ — от открытого файла: это та версия, которую прочел помощник.
	return true, x.ext.cache.Put(d.Path(), info.Size(), info.ModTime(), text)
}
