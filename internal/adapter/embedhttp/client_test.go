package embedhttp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/embedhttp"
)

func TestEmbed(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string][]float32{"embedding": {0.1, 0.2, 0.3}})
	}))
	defer srv.Close()

	v, err := embedhttp.New(srv.URL, "модель").Embed("запрос")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(v) != 3 {
		t.Fatalf("вектор длины %d, ожидалось 3", len(v))
	}
	if gotBody["model"] != "модель" || gotBody["prompt"] != "запрос" {
		t.Errorf("служба получила %+v — ожидались model=модель и prompt=запрос", gotBody)
	}
}

// Каждый отказ обязан назвать причину: пустой вектор, отданный молча, поиск
// честно ни с чем не сравнит, и человек прочитает это как «в базе такого нет».
func TestEmbed_failures(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantIn  string
	}{
		{"служба ответила ошибкой", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}, "503"},
		{"ответ не разобрать", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("не json"))
		}, "разобрать"},
		{"вектор пуст", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"embedding": []}`))
		}, "пустой вектор"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			_, err := embedhttp.New(srv.URL, "модель").Embed("запрос")
			if err == nil {
				t.Fatal("ошибки нет, а должна быть")
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("ошибка %q не называет %q", err, tt.wantIn)
			}
		})
	}
}

func TestEmbed_serviceDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // служба выключена — самый частый случай на живой машине

	if _, err := embedhttp.New(url, "модель").Embed("запрос"); err == nil {
		t.Fatal("служба выключена, а ошибки нет")
	}
}
