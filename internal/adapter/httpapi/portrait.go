package httpapi

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/portraitjson"
)

// maxPortraitBody bounds a portrait: факт до 300 знаков, сотни фактов — десятки КБ.
const maxPortraitBody = 1 << 20

// PortraitStore keeps one person's portrait (#479) with its version. Get без
// портрета — blob.ErrNotFound; Put с пустым ifMatch создает, иначе пишет
// поверх прочитанной версии, чужая версия — blob.ErrConflict.
type PortraitStore interface {
	Get(ctx context.Context) (blob.Object, error)
	Put(ctx context.Context, data []byte, ifMatch string) (string, error)
}

// WithPortrait подключает портрет «Что DVK помнит обо мне». Без него маршрута нет.
func WithPortrait(s PortraitStore) Option {
	return func(o *options) { o.portrait = s }
}

// handleGetPortrait returns the portrait and its version in ETag; портрета
// еще нет — пустой, без ETag (окно тогда создает его с If-None-Match: *).
func handleGetPortrait(s PortraitStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		o, err := s.Get(r.Context())
		switch {
		case errors.Is(err, blob.ErrNotFound):
			o = blob.Object{Data: []byte(`{"facts":[]}`)}
		case err != nil:
			log.Printf("portrait read: %v", err)
			http.Error(w, "portrait unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if o.ETag != "" {
			w.Header().Set("ETag", o.ETag)
		}
		_, _ = w.Write(o.Data)
	}
}

// handlePutPortrait writes the portrait from the version the writer read:
// If-Match — версия, If-None-Match: * — создать. Без условия запись вслепую
// затерла бы правку с другого устройства, поэтому 428.
func handlePutPortrait(s PortraitStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ifMatch := r.Header.Get("If-Match")
		if ifMatch == "" && r.Header.Get("If-None-Match") != "*" {
			http.Error(w, "If-Match or If-None-Match: * required", http.StatusPreconditionRequired)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPortraitBody))
		if err != nil {
			http.Error(w, "portrait too large", http.StatusRequestEntityTooLarge)
			return
		}
		p, err := portraitjson.Decode(raw)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		data, err := portraitjson.Encode(p) // в бакет — проверенное, а не присланное
		if err != nil {
			http.Error(w, "portrait not encoded", http.StatusInternalServerError)
			return
		}
		etag, err := s.Put(r.Context(), data, ifMatch)
		switch {
		case errors.Is(err, blob.ErrConflict):
			http.Error(w, "portrait changed meanwhile", http.StatusPreconditionFailed)
			return
		case err != nil:
			log.Printf("portrait write: %v", err)
			http.Error(w, "portrait not saved", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNoContent)
	}
}
