package phonepage_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/phonepage"
)

const fp = "D1:32:A9:ED:B6:87:63:E7:0A:97:F5:54:3D:DE:0F:27:AB:42:BE:21:B7:52:64:32:E8:B4:73:11:DD:24:EA:9E"

// Приложение Android (TWA) открывается во весь экран, только если сайт
// подтверждает его: /.well-known/assetlinks.json с пакетом и отпечатком ключа.
func TestAssetLinks(t *testing.T) {
	h := phonepage.AssetLinks("ru.daniilvdovin.dvk:" + fp)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("код %d, тип %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var got []struct {
		Relation []string `json:"relation"`
		Target   struct {
			Namespace    string   `json:"namespace"`
			Package      string   `json:"package_name"`
			Fingerprints []string `json:"sha256_cert_fingerprints"`
		} `json:"target"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Relation[0] != "delegate_permission/common.handle_all_urls" || got[0].Target.Namespace != "android_app" ||
		got[0].Target.Package != "ru.daniilvdovin.dvk" || len(got[0].Target.Fingerprints) != 1 || got[0].Target.Fingerprints[0] != fp {
		t.Fatalf("ответ %s", rec.Body)
	}
}

// Не задано или задано криво — 404, а не файл с мусором: Android перепроверяет
// и не должен поверить чужому пакету.
func TestAssetLinksRefusesBadSpec(t *testing.T) {
	for _, spec := range []string{"", "ru.dvk", "ru.dvk:zz", "ru dvk:" + fp, "ru.dvk:" + fp[:20], "ru.dvk:" + strings.ToLower(fp)} {
		rec := httptest.NewRecorder()
		phonepage.AssetLinks(spec).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%q: код %d", spec, rec.Code)
		}
	}
}
