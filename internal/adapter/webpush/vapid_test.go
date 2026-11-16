package webpush

import (
	"context"
	"sync"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob/blobfs"
)

// Ключ создается один раз и переживает перезапуск: смена ключа отменила бы
// все подписки телефонов.
func TestLoadVAPID(t *testing.T) {
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := LoadVAPID(ctx, s, "mailto:hi@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(b64(t, a.PublicKey())) != 65 {
		t.Fatalf("открытый ключ %q", a.PublicKey())
	}
	b, err := LoadVAPID(ctx, s, "mailto:hi@example.com")
	if err != nil || b.PublicKey() != a.PublicKey() {
		t.Fatalf("после перезапуска другой ключ: %v", err)
	}
	if _, err := LoadVAPID(ctx, s, "nobody"); err == nil {
		t.Error("контакт без схемы принят")
	}
}

// Два экземпляра сервера стартуют разом — оба сходятся на одном ключе.
func TestLoadVAPID_concurrentStart(t *testing.T) {
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Go(func() {
			v, err := LoadVAPID(ctx, s, "mailto:hi@example.com")
			if err != nil {
				t.Error(err)
				return
			}
			keys[i] = v.PublicKey()
		})
	}
	wg.Wait()
	for _, k := range keys {
		if k != keys[0] || k == "" {
			t.Fatalf("ключи разошлись: %v", keys)
		}
	}
}

func TestLoadVAPID_corrupt(t *testing.T) {
	ctx := context.Background()
	s, err := blobfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, VAPIDKey, []byte(`{"d":"AAAA"}`), ""); err != nil {
		t.Fatal(err)
	}
	// Испорченный ключ не подменяется новым молча: это отменило бы подписки.
	if _, err := LoadVAPID(ctx, s, "mailto:hi@example.com"); err == nil {
		t.Fatal("испорченный ключ принят")
	}
	if o, _ := s.Get(ctx, VAPIDKey); string(o.Data) != `{"d":"AAAA"}` {
		t.Fatalf("ключ перезаписан: %s", o.Data)
	}
}
