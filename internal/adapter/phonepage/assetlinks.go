package phonepage

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// Приложение DVK для Android — Trusted Web Activity над /m/ (mobile/android).
// Chrome показывает его во весь экран, только если сайт подтверждает пакет и
// отпечаток ключа подписи: /.well-known/assetlinks.json (Digital Asset Links).

var (
	pkgRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	fpRe  = regexp.MustCompile(`^[0-9A-F]{2}(:[0-9A-F]{2}){31}$`)
)

// AssetLinks serves the statement for spec «пакет:отпечаток[,отпечаток]» (SHA-256
// ключа подписи в записи keytool: AA:BB:…). Пусто или криво — 404: Android не
// должен поверить чужому пакету или обрывку отпечатка.
func AssetLinks(spec string) http.Handler {
	pkg, fps, ok := strings.Cut(spec, ":")
	list := strings.Split(fps, ",")
	if !ok || !pkgRe.MatchString(pkg) {
		return http.NotFoundHandler()
	}
	for _, f := range list {
		if !fpRe.MatchString(f) {
			return http.NotFoundHandler()
		}
	}
	type target struct {
		Namespace    string   `json:"namespace"`
		Package      string   `json:"package_name"`
		Fingerprints []string `json:"sha256_cert_fingerprints"`
	}
	type statement struct {
		Relation []string `json:"relation"`
		Target   target   `json:"target"`
	}
	body, _ := json.Marshal([]statement{{Relation: []string{"delegate_permission/common.handle_all_urls"}, Target: target{Namespace: "android_app", Package: pkg, Fingerprints: list}}})
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		_, _ = w.Write(body)
	})
}
