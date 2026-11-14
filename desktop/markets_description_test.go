package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/markets"
)

// fakeOzonDesc answers the card with its annotation, gender and color (замер 15.11).
func fakeOzonDesc(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Api-Key") != "k-1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"result":[{"name":"Футболка мужская черная","attributes":[` +
			`{"id":4191,"values":[{"value":"Женская бежевая футболка оверсайз."}]},` +
			`{"id":9163,"values":[{"value":"Мужской"}]},{"id":10096,"values":[{"value":"черный"}]}]}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// «Описание» (#611): расхождения с полом и цветом карточки — первыми.
func TestDescriptionView(t *testing.T) {
	s, mp := titleSettings(t, false, titleCard)
	oz := fakeOzonDesc(t)
	row, err := s.AddOzon("Магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.descriptionCheck(context.Background(), mp.Client(), oz.URL, row.ID, "1234567", false)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, m := range v.Mismatch {
		kinds[m.Kind] = m.Found
	}
	if kinds["gender"] != "женская" || kinds["color"] != "бежевая" || v.Description != "Женская бежевая футболка оверсайз." || v.Main == "" {
		t.Fatalf("view %+v", v)
	}
}

// Новое описание пишет модель окна, и только с согласием: без него в модель ничего не уходит.
func TestModelDescription(t *testing.T) {
	cd, m := localChat(t)
	in := markets.DescriptionInput{Title: "Футболка мужская черная", Gender: "Мужской", Color: "черный", Description: "Женская бежевая футболка."}
	if _, err := cd.modelDescription(in, markets.CheckDescription(in), false); err == nil || m.n != 0 {
		t.Fatalf("без согласия: %v, вызовов %d", err, m.n)
	}
	m.reply = "  Черная мужская футболка.  "
	got, err := cd.modelDescription(in, markets.CheckDescription(in), true)
	if err != nil || got.Text != "Черная мужская футболка." || m.n != 1 || got.Model == "" {
		t.Fatalf("draft %+v, %v, вызовов %d", got, err, m.n)
	}
	var prompt strings.Builder
	for _, msg := range m.asked[0] {
		prompt.WriteString(msg.Content)
	}
	if !strings.Contains(prompt.String(), "Футболка мужская черная") || !strings.Contains(prompt.String(), "не выдумывай") {
		t.Errorf("prompt:\n%s", prompt.String())
	}
}
