package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/imagegen"
	"github.com/daniil/kb-engine/internal/domain"
)

// Модель изображений настраивается человеком: адрес, имя, ключ в Связке; в
// файл конфига ключ не попадает; пустое — отключить, ключ стирается (#678).
func TestImageSettings(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	if v := s.imageSettings(); v.URL != "" || v.HasKey {
		t.Fatalf("fresh %+v", v)
	}
	if err := s.setImageSettings("https://ai-gateway.vercel.sh/v1", "google/gemini-3.1-flash-image", "k-secret"); err != nil {
		t.Fatal(err)
	}
	v := s.imageSettings()
	if v.URL != "https://ai-gateway.vercel.sh/v1" || v.Model != "google/gemini-3.1-flash-image" || !v.HasKey || tok.m[imageKeyAccount] != "k-secret" {
		t.Fatalf("%+v key %q", v, tok.m[imageKeyAccount])
	}
	if raw, _ := os.ReadFile(s.path); strings.Contains(string(raw), "k-secret") {
		t.Error("the key is in the config file")
	}
	if err := s.setImageSettings("https://ai-gateway.vercel.sh/v1", "other-model", ""); err != nil || tok.m[imageKeyAccount] != "k-secret" {
		t.Errorf("empty key keeps the old one: %v %q", err, tok.m[imageKeyAccount])
	}
	if err := s.setImageSettings("http://evil.example/v1", "m", "x"); err == nil {
		t.Error("plain http to a foreign host accepted")
	}
	if tok.m[imageKeyAccount] != "k-secret" {
		t.Error("a refused save changed the key")
	}
	if err := s.setImageSettings("", "", ""); err != nil {
		t.Fatal(err)
	}
	if v := s.imageSettings(); v.URL != "" || v.HasKey {
		t.Errorf("not cleared %+v", v)
	}
	if _, ok := tok.m[imageKeyAccount]; ok {
		t.Error("the key stays after disconnecting")
	}
}

// Инфографика своей моделью: главное фото карточки — референс, картинка
// приходит сразу data URL, вызов попадает в журнал (#678).
func TestOwnInfographic(t *testing.T) {
	s, oz, cab, _, _ := studioFake(t, 0, 0)
	var seen string
	calls := &memCalls{}
	var gotRefs [][]byte
	draw := imageDesk{draw: refDraw{refs: &gotRefs, img: imagegen.Image{Data: []byte("PNGDATA"), MIME: "image/png", Cost: "0.068"}}, model: "m", calls: calls}
	res, err := s.ownInfographic(context.Background(), nil, oz, cab, "1234567", "  нарисуй  ", draw, fakeFetch(&seen))
	if err != nil {
		t.Fatal(err)
	}
	if seen != "https://ir.ozone.ru/p.jpg" || len(gotRefs) != 1 {
		t.Errorf("fetched %q refs %d", seen, len(gotRefs))
	}
	if !strings.HasPrefix(res.Data, "data:image/png;base64,") || res.Model != "m" {
		t.Errorf("%+v", res)
	}
	if len(calls.calls) != 1 || calls.calls[0].Purpose() != domain.CallImage {
		t.Errorf("journal %v", calls.calls)
	}
	if _, err := s.ownInfographic(context.Background(), nil, oz, cab, "1234567", "   ", draw, fakeFetch(&seen)); err == nil {
		t.Error("empty prompt accepted")
	}
	if _, err := s.ownInfographic(context.Background(), nil, oz, cab, "abc", "x", draw, fakeFetch(&seen)); err == nil {
		t.Error("a non-numeric SKU accepted")
	}
	_ = json.Marshal
}

type refDraw struct {
	refs *[][]byte
	img  imagegen.Image
}

func (r refDraw) Generate(_ context.Context, _ string, refs [][]byte) (imagegen.Image, error) {
	*r.refs = refs
	return r.img, nil
}
