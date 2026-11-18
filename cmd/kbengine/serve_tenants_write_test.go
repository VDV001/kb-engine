package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const (
	editedByB = "EDITED-BY-B-4e8a"
	addedByB  = "ADDED-BY-B-52c1"
	spentByB  = "SPENT-BY-B-77d0"
)

// Запись одного человека не меняет базу другого (#545): TestTenantsAreIsolatedOnEveryRoute
// обходит только GET. У обоих в каталоге запись с id 1, поэтому правка «по номеру»
// от B проверяет, что номер ищется в базе B, а не в общей.
func TestTenantsWritesStayInOwnBase(t *testing.T) {
	f := newTenants(t)
	if rec := do(f.h, http.MethodPatch, "/api/entries/1", `{"title":"`+editedByB+`"}`, f.tokB); rec.Code != http.StatusOK {
		t.Fatalf("B edits own entry 1: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(f.h, http.MethodPost, "/api/entries",
		`{"title":"`+addedByB+`","category":"golang","url":"https://h/`+addedByB+`","verdict":"keep"}`, f.tokB); rec.Code != http.StatusCreated {
		t.Fatalf("B adds an entry: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(f.h, http.MethodPost, "/api/finances/transactions",
		`{"kind":"expense","amount":"77","category":"Транспорт","place":"`+spentByB+`","description":"`+spentByB+`","date":"2026-04-07"}`, f.tokB); rec.Code != http.StatusCreated {
		t.Fatalf("B adds a transaction: %d %s", rec.Code, rec.Body.String())
	}

	// Положительный контроль: свои правки B видит, иначе «у A не видно» ничего не значит.
	seenByB := 0
	for _, p := range registeredGETs(t) {
		body := readable(t, get(f.h, p, f.tokB))
		for _, mark := range []string{editedByB, addedByB, spentByB} {
			if strings.Contains(body, mark) {
				seenByB++
			}
		}
		bodyA := readable(t, get(f.h, p, f.tokA))
		for _, mark := range []string{editedByB, addedByB, spentByB} {
			if strings.Contains(bodyA, mark) {
				t.Errorf("%s with A's token shows B's write %q", p, mark)
			}
		}
	}
	if seenByB < 3 {
		t.Fatalf("B's writes visible %d times to B — the check would pass vacuously", seenByB)
	}
	// Запись 1 у A цела.
	if body := readable(t, get(f.h, "/api/entries", f.tokA)); !strings.Contains(body, secretA) || strings.Contains(body, editedByB) {
		t.Errorf("A's entries after B's PATCH of id 1: %s", body)
	}
}

// Файл ящика другого человека не читается и не удаляется, а подделанный id отвергается
// до хранилища (#545). Чтение чужого уже стоит в TestMailFiles; здесь удаление и подделка.
func TestTenantsMailFileForeignAccess(t *testing.T) {
	f := newTenants(t)
	frozen(f)
	_, phone := addPhone(t, f, f.tokA)
	piece := []byte("sealed-file-piece-of-A")
	rec := putFile(f, piece, phone)
	if rec.Code != http.StatusCreated {
		t.Fatalf("phone put: %d %s", rec.Code, rec.Body.String())
	}
	var out struct{ ID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == "" {
		t.Fatalf("put answer %s, %v", rec.Body.String(), err)
	}

	// Удаление идемпотентно («нет его — не ошибка»): для B это ключ в его же
	// пространстве, 204 ожидаем; важно, что файл A остался.
	if rec := do(f.h, http.MethodDelete, "/api/mail/files/"+out.ID, "", f.tokB); rec.Code >= 500 {
		t.Errorf("B deletes by A's id: %d", rec.Code)
	}
	if rec := do(f.h, http.MethodGet, "/api/mail/files/"+out.ID, "", f.tokA); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), piece) {
		t.Errorf("A's file after B's delete: %d %q", rec.Code, rec.Body.String())
	}

	forged := []string{
		"../" + out.ID,
		url.PathEscape("../") + out.ID,
		"%2e%2e%2f" + out.ID,
		"users/alpha/" + out.ID,
		out.ID[:len(out.ID)-1],
		out.ID + "0",
		strings.ToUpper(out.ID),
		"zz" + out.ID[2:],
	}
	for _, id := range forged {
		for _, method := range []string{http.MethodGet, http.MethodDelete} {
			rec := do(f.h, method, "/api/mail/files/"+id, "", f.tokB)
			if rec.Code >= 500 || (method == http.MethodGet && rec.Code == http.StatusOK) {
				t.Errorf("%s forged id %q as B: %d", method, id, rec.Code)
			}
		}
	}
	if rec := do(f.h, http.MethodGet, "/api/mail/files/"+out.ID, "", f.tokA); rec.Code != http.StatusOK {
		t.Errorf("A's file after forged ids: %d", rec.Code)
	}
}
