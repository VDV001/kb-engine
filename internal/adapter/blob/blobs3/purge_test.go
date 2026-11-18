package blobs3_test

import (
	"crypto/rand"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobs3"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobtest"
)

// version is one stored version of a key; marker — метка удаления, как ее
// ставит DeleteObject без versionId в бакете с версиями.
type version struct {
	id     string
	marker bool
	data   []byte
}

// fakeVersioned models a versioned bucket the way Object Storage documents
// it: DELETE без versionId ставит метку и оставляет прежние версии, DELETE с
// versionId убирает именно эту, ListObjectVersions (`?versions`) отдает версии
// и метки страницами по pageSize. Как и fakeS3, это модель того же автора,
// поэтому живая проверка — TestLiveYandex.
type fakeVersioned struct {
	mu       sync.Mutex
	keys     map[string][]version // новые версии впереди
	next     int
	pageSize int
	// rogue — запись, которую список показывает, хотя она не под префиксом:
	// ответ сервера не должен заставить адаптер удалить чужое.
	rogue *version
	// ignoreVersionDeletes — DELETE с versionId отвечает 204, но ничего не убирает.
	ignoreVersionDeletes bool
	// blankVersionID — список не называет версии: удалять по ним нечем.
	blankVersionID bool
	deleted        []string // «key@versionId» удаленных по версии
	// weird — ключи под префиксом, которые список показывает, а путь не выдержит
	// (`..`, пустой сегмент): удалить их через адрес нельзя.
	weird []string
	// endless — каждый круг списка начинается с новой записи под префиксом, а
	// удаление отвечает 204: враждебный сервер или бесконечный писатель.
	endless bool
}

func newFakeVersioned(t *testing.T, pageSize int) (*fakeVersioned, *httptest.Server) {
	t.Helper()
	f := &fakeVersioned{keys: map[string][]version{}, pageSize: pageSize}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeVersioned) newID() string { f.next++; return "v" + strconv.Itoa(f.next) }

func (f *fakeVersioned) live(key string) (version, bool) {
	vs := f.keys[key]
	if len(vs) == 0 || vs[0].marker {
		return version{}, false
	}
	return vs[0], true
}

func (f *fakeVersioned) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+token {
		http.Error(w, "AccessDenied", http.StatusForbidden)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	if _, ok := q["versions"]; ok && r.Method == http.MethodGet {
		f.list(w, q.Get("prefix"), q.Get("key-marker"), q.Get("version-id-marker"))
		return
	}
	key, ok := strings.CutPrefix(r.URL.Path, "/"+bucket+"/")
	if !ok {
		http.Error(w, "NoSuchBucket", http.StatusNotFound)
		return
	}
	cur, exists := f.live(key)
	switch r.Method {
	case http.MethodGet:
		if !exists {
			http.Error(w, "NoSuchKey", http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", etagOf(cur.data))
		_, _ = w.Write(cur.data)
	case http.MethodPut:
		ifMatch, ifNone := r.Header.Get("If-Match"), r.Header.Get("If-None-Match")
		switch {
		case ifNone == "*" && exists, ifMatch != "" && (!exists || ifMatch != etagOf(cur.data)):
			http.Error(w, "PreconditionFailed", http.StatusPreconditionFailed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.keys[key] = append([]version{{id: f.newID(), data: body}}, f.keys[key]...)
		w.Header().Set("ETag", etagOf(body))
	case http.MethodDelete:
		id := q.Get("versionId")
		if id == "" {
			if len(f.keys[key]) > 0 {
				f.keys[key] = append([]version{{id: f.newID(), marker: true}}, f.keys[key]...)
			}
		} else if !f.ignoreVersionDeletes {
			f.keys[key] = slices.DeleteFunc(f.keys[key], func(v version) bool { return v.id == id })
			if len(f.keys[key]) == 0 {
				delete(f.keys, key)
			}
			f.deleted = append(f.deleted, key+"@"+id)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

type listEntry struct {
	Key       string `xml:"Key"`
	VersionID string `xml:"VersionId"`
	IsLatest  bool   `xml:"IsLatest"`
}

type listResult struct {
	XMLName             xml.Name    `xml:"ListVersionsResult"`
	IsTruncated         bool        `xml:"IsTruncated"`
	NextKeyMarker       string      `xml:"NextKeyMarker,omitempty"`
	NextVersionIDMarker string      `xml:"NextVersionIdMarker,omitempty"`
	Version             []listEntry `xml:"Version"`
	DeleteMarker        []listEntry `xml:"DeleteMarker"`
}

// list answers `?versions`: ключи по порядку, у ключа версии от новой к старой,
// страница — не больше pageSize записей, продолжение — по паре маркеров.
func (f *fakeVersioned) list(w http.ResponseWriter, prefix, keyMarker, idMarker string) {
	type row struct {
		key string
		v   version
		new bool
	}
	var rows []row
	for _, key := range slices.Sorted(func(yield func(string) bool) {
		for k := range f.keys {
			if !yield(k) {
				return
			}
		}
	}) {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		for i, v := range f.keys[key] {
			rows = append(rows, row{key, v, i == 0})
		}
	}
	if f.rogue != nil {
		rows = append(rows, row{"users/other/kb/x", *f.rogue, true})
	}
	for i, k := range f.weird {
		rows = append(rows, row{k, version{id: "weird-" + strconv.Itoa(i)}, true})
	}
	if f.endless {
		f.next++
		rows = append([]row{{prefix + "endless", version{id: "e" + strconv.Itoa(f.next)}, true}}, rows...)
	}
	start := 0
	if keyMarker != "" {
		for i, rw := range rows {
			if rw.key == keyMarker && rw.v.id == idMarker {
				start = i + 1
				break
			}
		}
	}
	rows = rows[start:]
	res := listResult{}
	if f.pageSize > 0 && len(rows) > f.pageSize {
		res.IsTruncated = true
		last := rows[f.pageSize-1]
		res.NextKeyMarker, res.NextVersionIDMarker = last.key, last.v.id
		rows = rows[:f.pageSize]
	}
	for _, rw := range rows {
		e := listEntry{Key: rw.key, VersionID: rw.v.id, IsLatest: rw.new}
		if f.blankVersionID {
			e.VersionID = ""
		}
		if rw.v.marker {
			res.DeleteMarker = append(res.DeleteMarker, e)
		} else {
			res.Version = append(res.Version, e)
		}
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(res)
}

// Общий контракт: Purge на бакете с версиями и страницами по 2 записи.
func TestPurgeContract(t *testing.T) {
	blobtest.RunPurger(t, func(t *testing.T) blobtest.PurgeStore {
		_, srv := newFakeVersioned(t, 2)
		return newStore(t, srv.URL, blobs3.StaticToken(token))
	})
}

// Главное, ради чего Purge не равен Delete: уходят и старые версии, и метки
// удаления, а не только текущая версия; у соседа версии целы.
func TestPurgeRemovesEveryVersionAndMarker(t *testing.T) {
	f, srv := newFakeVersioned(t, 3)
	s := newStore(t, srv.URL, blobs3.StaticToken(token))
	ctx := t.Context()
	put := func(key, data, ifMatch string) string {
		t.Helper()
		etag, err := s.Put(ctx, key, []byte(data), ifMatch)
		if err != nil {
			t.Fatal(err)
		}
		return etag
	}
	e := put("users/a/kb/x", "v1", "")
	e = put("users/a/kb/x", "v2", e)
	put("users/a/kb/x", "v3", e)
	put("users/a/mail/f", "m", "")
	if err := s.Delete(ctx, "users/a/mail/f"); err != nil { // метка поверх версии
		t.Fatal(err)
	}
	put("users/a-b/kb/x", "keep", "")

	n, err := s.Purge(ctx, "users/a/")
	if err != nil || n != 2 {
		t.Fatalf("Purge = %d, %v; want 2 keys, nil", n, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, vs := range f.keys {
		if strings.HasPrefix(key, "users/a/") {
			t.Errorf("%s still has %d versions or markers", key, len(vs))
		}
	}
	if len(f.keys["users/a-b/kb/x"]) != 1 {
		t.Errorf("neighbour versions: %d, want 1", len(f.keys["users/a-b/kb/x"]))
	}
	if len(f.deleted) != 5 { // три версии x, версия и метка f
		t.Errorf("deleted by version %v, want 5 entries", f.deleted)
	}
}

// Бакет с общим префиксом адаптера: Purge не выходит за него.
func TestPurgeStaysUnderTheStorePrefix(t *testing.T) {
	f, srv := newFakeVersioned(t, 0)
	s, err := blobs3.New(blobs3.Config{Endpoint: srv.URL, Bucket: bucket, Token: blobs3.StaticToken(token), Prefix: "p/"})
	if err != nil {
		t.Fatal(err)
	}
	outside := newStore(t, srv.URL, blobs3.StaticToken(token))
	for _, key := range []string{"users/a/kb/x"} {
		if _, err := outside.Put(t.Context(), key, []byte("outside"), ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Put(t.Context(), "users/a/kb/x", []byte("inside"), ""); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Purge(t.Context(), "users/a/"); err != nil || n != 1 {
		t.Fatalf("Purge = %d, %v", n, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.keys["users/a/kb/x"]; !ok {
		t.Error("the key outside the store prefix was purged")
	}
	if _, ok := f.keys["p/users/a/kb/x"]; ok {
		t.Error("the key inside the store prefix is still there")
	}
}

// Ответ списка с ключом не под префиксом не заставляет удалять чужое.
func TestPurgeRefusesAKeyOutsideThePrefix(t *testing.T) {
	f, srv := newFakeVersioned(t, 0)
	s := newStore(t, srv.URL, blobs3.StaticToken(token))
	if _, err := s.Put(t.Context(), "users/a/kb/x", []byte("x"), ""); err != nil {
		t.Fatal(err)
	}
	f.rogue = &version{id: "rogue-1"}
	if _, err := s.Purge(t.Context(), "users/a/"); err == nil {
		t.Fatal("Purge accepted a listing with a foreign key")
	}
	for _, d := range f.deleted {
		if strings.HasPrefix(d, "users/other/") {
			t.Errorf("deleted %s", d)
		}
	}
}

// Удаление, которое отвечает 204 и ничего не убирает, не крутит цикл вечно.
func TestPurgeStopsWhenDeletesMakeNoProgress(t *testing.T) {
	f, srv := newFakeVersioned(t, 0)
	s := newStore(t, srv.URL, blobs3.StaticToken(token))
	if _, err := s.Put(t.Context(), "users/a/kb/x", []byte("x"), ""); err != nil {
		t.Fatal(err)
	}
	f.ignoreVersionDeletes = true
	if _, err := s.Purge(t.Context(), "users/a/"); err == nil {
		t.Fatal("Purge reported success while the version is still there")
	}
}

// Ошибка доступа и сбой списка — ошибка, а не «удалено 0».
func TestPurgeReportsAccessErrors(t *testing.T) {
	_, srv := newFakeVersioned(t, 0)
	s := newStore(t, srv.URL, blobs3.StaticToken("wrong"))
	n, err := s.Purge(t.Context(), "users/a/")
	if err == nil || n != 0 {
		t.Fatalf("Purge = %d, %v; want an error", n, err)
	}
	if errors.Is(err, blob.ErrInvalidKey) {
		t.Errorf("an access error is not an invalid key: %v", err)
	}
}

// Список без versionId не превращается в бесконечные метки удаления: Delete
// без версии ставит метку, а не убирает запись.
func TestPurgeRefusesAnEntryWithoutVersionID(t *testing.T) {
	f, srv := newFakeVersioned(t, 0)
	s := newStore(t, srv.URL, blobs3.StaticToken(token))
	if _, err := s.Put(t.Context(), "users/a/kb/x", []byte("x"), ""); err != nil {
		t.Fatal(err)
	}
	f.blankVersionID = true
	if _, err := s.Purge(t.Context(), "users/a/"); err == nil {
		t.Fatal("Purge went on with a listing that has no version ids")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.keys["users/a/kb/x"]) != 1 {
		t.Errorf("versions of the key: %d, want 1 untouched", len(f.keys["users/a/kb/x"]))
	}
}

// TestLiveYandexPurge runs the purge contract on a real versioned bucket: то,
// что fakeVersioned моделирует (метка вместо удаления, версии по одной,
// страницы списка), здесь проверяет сам бакет. Те же KB_BLOB_LIVE_BUCKET и
// KB_BLOB_LIVE_TOKEN_FILE, что у TestLiveYandex; ключи только под
// test/<случайное>/, их чистит правило жизненного цикла.
func TestLiveYandexPurge(t *testing.T) {
	bkt, tokFile := os.Getenv("KB_BLOB_LIVE_BUCKET"), os.Getenv("KB_BLOB_LIVE_TOKEN_FILE")
	if bkt == "" || tokFile == "" {
		t.Skip("KB_BLOB_LIVE_BUCKET / KB_BLOB_LIVE_TOKEN_FILE not set")
	}
	raw, err := os.ReadFile(tokFile)
	if err != nil {
		t.Fatal(err)
	}
	tok := blobs3.StaticToken(strings.TrimSpace(string(raw)))
	run := rand.Text()[:12]
	var n atomic.Int64
	blobtest.RunPurger(t, func(t *testing.T) blobtest.PurgeStore {
		s, err := blobs3.New(blobs3.Config{
			Endpoint: blobs3.YandexEndpoint,
			Bucket:   bkt,
			Token:    tok,
			Prefix:   fmt.Sprintf("test/purge-%s/%d/", run, n.Add(1)),
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// Главное свойство Purge на живом бакете: после перезаписи и метки удаления
// под префиксом не остается ни версий, ни меток. Контракт RunPurger этого не
// видит (Purge на метках тоже «уносит» ключ из Get), поэтому список версий
// читается напрямую.
func TestLiveYandexPurgeLeavesNoVersionsOrMarkers(t *testing.T) {
	bkt, tokFile := os.Getenv("KB_BLOB_LIVE_BUCKET"), os.Getenv("KB_BLOB_LIVE_TOKEN_FILE")
	if bkt == "" || tokFile == "" {
		t.Skip("KB_BLOB_LIVE_BUCKET / KB_BLOB_LIVE_TOKEN_FILE not set")
	}
	raw, err := os.ReadFile(tokFile)
	if err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimSpace(string(raw))
	prefix := fmt.Sprintf("test/purge-%s/versions/", rand.Text()[:12])
	s, err := blobs3.New(blobs3.Config{Endpoint: blobs3.YandexEndpoint, Bucket: bkt, Token: blobs3.StaticToken(tok), Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	etag, err := s.Put(ctx, "users/a/kb/x", []byte("v1"), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "users/a/kb/x", []byte("v2"), etag); err != nil { // вторая версия
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "users/a/mail/f", []byte("m"), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "users/a/mail/f"); err != nil { // метка удаления
		t.Fatal(err)
	}
	if n, err := s.Purge(ctx, "users/a/"); err != nil || n != 2 {
		t.Fatalf("Purge = %d, %v; want 2 keys, nil", n, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		blobs3.YandexEndpoint+"/"+bkt+"/?versions&prefix="+prefix+"users/a/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list versions: %d", resp.StatusCode)
	}
	// «<Version[ >]», а не «<Version»: в шапке ответа есть <VersionIdMarker>.
	if left := regexp.MustCompile(`<(?:Version|DeleteMarker)[ >]`).FindAllString(string(body), -1); len(left) != 0 {
		t.Errorf("%d versions or markers left after Purge: %s", len(left), body)
	}
}

// Ключ, которого через адрес не выразить (`..` в пути ушел бы в другой объект),
// не обрывает удаление человека: остальное уходит, а ошибка называет пропущенное.
func TestPurgeSkipsKeysItCannotAddressAndSaysSo(t *testing.T) {
	f, srv := newFakeVersioned(t, 0)
	s := newStore(t, srv.URL, blobs3.StaticToken(token))
	if _, err := s.Put(t.Context(), "users/a/kb/x", []byte("x"), ""); err != nil {
		t.Fatal(err)
	}
	f.weird = []string{"users/a/../auth/users.json", "users/a//e"}
	n, err := s.Purge(t.Context(), "users/a/")
	if err == nil || !strings.Contains(err.Error(), "users/a/../auth/users.json") {
		t.Fatalf("Purge = %d, %v; want an error naming the skipped key", n, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.keys["users/a/kb/x"]; ok {
		t.Error("the removable key was left because of the odd one")
	}
	for _, d := range f.deleted {
		if strings.Contains(d, "..") || strings.Contains(d, "//") {
			t.Errorf("deleted through an address that is not safe: %s", d)
		}
	}
}

// Круги не бесконечны: сервер, у которого каждый круг начинается с новой записи,
// останавливается по пределу кругов.
func TestPurgeStopsAfterTooManyRounds(t *testing.T) {
	f, srv := newFakeVersioned(t, 0)
	s := newStore(t, srv.URL, blobs3.StaticToken(token))
	f.endless = true
	defer blobs3.SetMaxPurgeRounds(5)()
	if _, err := s.Purge(t.Context(), "users/a/"); err == nil || !strings.Contains(err.Error(), "rounds") {
		t.Fatalf("Purge on an endless listing: %v, want an error about rounds", err)
	}
}
