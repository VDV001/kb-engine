package main

import (
	"strings"
	"testing"
)

func TestMoniumFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantOn  bool
		wantErr string // подстрока ошибки; пусто — без ошибки
	}{
		{"nothing set: off", nil, false, ""},
		{"key without project", map[string]string{"KB_MONIUM_API_KEY": "k"}, false, "KB_MONIUM_PROJECT"},
		{"project without key", map[string]string{"KB_MONIUM_PROJECT": "folder__x"}, false, "KB_MONIUM_API_KEY"},
		{"plain http endpoint would leak the key", map[string]string{
			"KB_MONIUM_API_KEY": "k", "KB_MONIUM_PROJECT": "folder__x", "KB_MONIUM_ENDPOINT": "http://ingest.example/otlp/v1/logs",
		}, false, "KB_MONIUM_ENDPOINT"},
		{"key and project: on", map[string]string{"KB_MONIUM_API_KEY": "k", "KB_MONIUM_PROJECT": "folder__x"}, true, ""},
		{"custom https endpoint: on", map[string]string{
			"KB_MONIUM_API_KEY": "k", "KB_MONIUM_PROJECT": "folder__x", "KB_MONIUM_ENDPOINT": "https://ingest.example/otlp/v1/logs",
		}, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, err := moniumFromEnv(func(k string) string { return tc.env[k] })
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to name %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (w != nil) != tc.wantOn {
				t.Errorf("writer on = %v, want %v", w != nil, tc.wantOn)
			}
		})
	}
}
