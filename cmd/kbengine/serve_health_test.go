package main

import (
	"io"
	"net/http"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
)

// Строка о потерях логов нужна пробе здоровья: если Monium недоступен, больше этого не
// увидит никто (stdout контейнера в облаке не читается). Пробы 5 минут и UptimeRobot
// смотрят на код 200, поэтому первая строка остается «ok» (#697).
func TestHealthzReportsDroppedLogLines(t *testing.T) {
	store, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dropped := func(n uint64) tenantOption { return withLogDropped(func() uint64 { return n }) }
	tests := []struct {
		name string
		opts []tenantOption
		want string
	}{
		{"monium off: the plain answer is unchanged", nil, "ok\n"},
		{"monium on, nothing lost", []tenantOption{dropped(0)}, "ok\nlog-dropped: 0\n"},
		{"monium on, seven lost", []tenantOption{dropped(7)}, "ok\nlog-dropped: 7\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := tenantsHandler(t.Context(), store, io.Discard, tc.opts...)
			rec := get(h, "/healthz", "") // без токена, как у проб
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200", rec.Code)
			}
			if got := rec.Body.String(); got != tc.want {
				t.Errorf("body %q, want %q", got, tc.want)
			}
		})
	}
}
