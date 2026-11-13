package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// fakeMPStats answers like the MPStats API: ключ m-1, лимит 4 из 10 000.
func fakeMPStats(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Mpstats-TOKEN") != "m-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"available":10000,"use":4}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Ключ MPStats — подключение, как кабинет Ozon (отзыв владельца 13.11): ключ
// проверяется до сохранения и лежит только в Связке ключей.
func TestAddMPStats(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	srv := fakeMPStats(t)
	ep := checkEndpoints{MPStats: srv.URL, HTTP: srv.Client()}
	if _, err := s.addMPStats(context.Background(), ep, "MPStats", " "); err == nil {
		t.Error("empty key saved")
	}
	if _, err := s.addMPStats(context.Background(), ep, "MPStats", "чужой"); err == nil || !strings.Contains(err.Error(), "не принят") {
		t.Errorf("wrong key: %v", err)
	}
	if len(s.snapshot().cfg.Connections) != 0 || len(tok.m) != 0 {
		t.Fatalf("refused key left traces: %+v %v", s.snapshot().cfg.Connections, tok.m)
	}
	row, err := s.addMPStats(context.Background(), ep, " ", " m-1 ")
	if err != nil {
		t.Fatal(err)
	}
	if row.Kind != "mpstats" || row.Name != "MPStats" || !row.OK || tok.m[connKeyAccount(row.ID)] != "m-1" {
		t.Errorf("row = %+v, key %q", row, tok.m[connKeyAccount(row.ID)])
	}
	if c, _ := s.snapshot().cfg.Connection(row.ID); c.Kind != desktopconfig.ConnMPStats || strings.Contains(c.Name+c.Account+c.URL, "m-1") {
		t.Errorf("config = %+v; the key must not be there", c)
	}
}

// «Проверить» у каждого подключения (отзыв владельца 13.11): живой запрос туда,
// куда подключение ходит, и итог словами.
func TestCheckConnection(t *testing.T) {
	s, tok, _ := newServerSettings(t)
	oz, mp := fakeOzon(t), fakeMPStats(t)
	speech := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer s-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(speech.Close)
	ep := checkEndpoints{Ozon: oz.URL, MPStats: mp.URL, HTTP: oz.Client()}

	ozRow, err := s.AddOzon("Магазин", "42", "k-1", false)
	if err != nil {
		t.Fatal(err)
	}
	mpRow, err := s.addMPStats(context.Background(), ep, "MPStats", "m-1")
	if err != nil {
		t.Fatal(err)
	}
	spRow, err := s.AddSpeech("whisper", speech.URL, "", "s-1")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, id string
		ok       bool
		note     string
	}{
		{"кабинет Ozon", ozRow.ID, true, "ключ принят"},
		{"MPStats", mpRow.ID, true, "4 из 10"},
		{"распознавание речи", spRow.ID, true, "отвечает"},
		{"нет такого", "нет-такого", false, "не найдено"},
	}
	for _, tc := range tests {
		got := s.checkConn(context.Background(), tc.id, ep)
		if got.OK != tc.ok || !strings.Contains(got.Note, tc.note) {
			t.Errorf("%s: %+v, want ok=%v and %q", tc.name, got, tc.ok, tc.note)
		}
	}
	tok.m[connKeyAccount(ozRow.ID)] = "чужой"
	tok.m[connKeyAccount(mpRow.ID)] = "чужой"
	tok.m[connKeyAccount(spRow.ID)] = "чужой"
	for _, id := range []string{ozRow.ID, mpRow.ID, spRow.ID} {
		if got := s.checkConn(context.Background(), id, ep); got.OK || !strings.Contains(got.Note, "не принят") {
			t.Errorf("%s with a wrong key: %+v", id, got)
		}
	}
}
