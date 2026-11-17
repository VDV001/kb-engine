package selfupdate_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/selfupdate"
)

// fakeReleases is GitHub Releases in memory: путь → файл; asked — что спросили.
type fakeReleases struct {
	files  map[string][]byte
	asked  []string
	opened []string // через Open — потоком
	err    error
}

func (f *fakeReleases) Open(ctx context.Context, path string, limit int64) (io.ReadCloser, error) {
	f.opened = append(f.opened, path)
	b, err := f.Fetch(ctx, path, limit)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeReleases) Fetch(_ context.Context, path string, limit int64) ([]byte, error) {
	f.asked = append(f.asked, path)
	if f.err != nil {
		return nil, f.err
	}
	b, ok := f.files[path]
	if !ok {
		return nil, fmt.Errorf("404 %s", path)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: %d bytes over the limit %d", path, len(b), limit)
	}
	return b, nil
}

const (
	// Манифест — в постоянном выпуске-канале, а не в «latest»: выпуск только
	// сервера стал бы «latest» и без манифеста дал бы 404 всем окнам (ревью #672).
	manifestPath = "download/dvk-latest/dvk-manifest.json"
	sigPath      = "download/dvk-latest/dvk-manifest.json.sig"
	setupPath    = "download/v0.41.0/DVK-Setup.exe"
)

var setup = []byte("windows setup v0.41.0")

func manifest(version string, body []byte) []byte {
	s := sha256.Sum256(body)
	return fmt.Appendf(nil, `{"version":%q,"expires":%q,"assets":[{"platform":"windows-amd64","name":"DVK-Setup.exe","sha256":%q,"size":%d}]}`,
		version, time.Now().AddDate(0, 0, 60).UTC().Format(time.RFC3339), hex.EncodeToString(s[:]), len(body))
}

func key(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func release(m, sig []byte) *fakeReleases {
	return &fakeReleases{files: map[string][]byte{manifestPath: m, sigPath: sig, setupPath: setup}}
}

func updater(t *testing.T, src selfupdate.Source, pub ed25519.PublicKey, current string) selfupdate.Updater {
	t.Helper()
	v, err := domain.NewReleaseVersion(current)
	if err != nil {
		t.Fatal(err)
	}
	return selfupdate.Updater{Source: src, PublicKey: pub, Current: v, Platform: "windows-amd64"}
}

func TestCheckAndDownload(t *testing.T) {
	pub, priv := key(t)
	m := manifest("v0.41.0", setup)
	src := release(m, selfupdate.SignManifest(priv, m))
	u := updater(t, src, pub, "v0.40.0-11-g154839a6")
	offer, err := u.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !offer.Available() || offer.Version().String() != "v0.41.0" {
		t.Fatalf("offer %v %s", offer.Available(), offer.Version())
	}
	// Файл идет потоком в dst (у окна — временный файл), не в память (ревью #672).
	var got bytes.Buffer
	if err := u.Download(t.Context(), offer, &got); err != nil {
		t.Fatal(err)
	}
	if got.String() != string(setup) {
		t.Errorf("downloaded %q", got.String())
	}
	if len(src.opened) != 1 || src.opened[0] != setupPath {
		t.Errorf("opened %v — сборка читается потоком через Open", src.opened)
	}
	want := []string{manifestPath, sigPath, setupPath}
	if fmt.Sprint(src.asked) != fmt.Sprint(want) {
		t.Errorf("asked %v, want %v", src.asked, want)
	}
}

// Подпись не та — окно ничего не предлагает и ничего не качает.
func TestCheckRejectsBadSignature(t *testing.T) {
	pub, priv := key(t)
	_, stranger := key(t)
	m := manifest("v0.41.0", setup)
	evil := manifest("v0.41.0", []byte("someone else's code"))
	sig := selfupdate.SignManifest(priv, m)
	cases := map[string]*fakeReleases{
		"stranger's key":           release(m, selfupdate.SignManifest(stranger, m)),
		"manifest swapped":         release(evil, sig),
		"signature without prefix": release(m, ed25519.Sign(priv, m)),
		"short signature":          release(m, sig[:63]),
		"empty signature":          release(m, nil),
	}
	for name, src := range cases {
		offer, err := updater(t, src, pub, "v0.40.0").Check(t.Context())
		if !errors.Is(err, domain.ErrReleaseSignature) || offer.Available() {
			t.Errorf("%s: available %v, %v; want ErrReleaseSignature", name, offer.Available(), err)
		}
		if len(src.asked) != 2 {
			t.Errorf("%s: asked %v — nothing past the manifest and signature", name, src.asked)
		}
	}
	// Окно без вшитого ключа не верит ничему.
	if _, err := updater(t, release(m, sig), nil, "v0.40.0").Check(t.Context()); !errors.Is(err, domain.ErrReleaseSignature) {
		t.Errorf("no public key: %v", err)
	}
}

// Подписанный, но старый или тот же выпуск — не обновление: откат запрещен.
func TestCheckNotNewer(t *testing.T) {
	pub, priv := key(t)
	for _, v := range []string{"v0.40.0", "v0.39.5"} {
		m := manifest(v, setup)
		offer, err := updater(t, release(m, selfupdate.SignManifest(priv, m)), pub, "v0.40.0").Check(t.Context())
		if err != nil || offer.Available() {
			t.Errorf("%s over v0.40.0: available %v, %v", v, offer.Available(), err)
		}
	}
}

func TestCheckNoAssetForPlatform(t *testing.T) {
	pub, priv := key(t)
	m := manifest("v0.41.0", setup)
	u := updater(t, release(m, selfupdate.SignManifest(priv, m)), pub, "v0.40.0")
	u.Platform = "darwin-arm64"
	if offer, err := u.Check(t.Context()); !errors.Is(err, domain.ErrNoReleaseAsset) || offer.Available() {
		t.Errorf("available %v, %v; want ErrNoReleaseAsset", offer.Available(), err)
	}
}

// Подмененный установщик при верном манифесте — отказ; лишнего не читаем.
func TestDownloadRejectsSubstitutedFile(t *testing.T) {
	pub, priv := key(t)
	m := manifest("v0.41.0", setup)
	cases := map[string][]byte{
		"same size, other bytes": []byte("windows sEtup v0.41.0"),
		"longer":                 append(append([]byte(nil), setup...), "!"...),
		"shorter":                setup[:4],
	}
	for name, body := range cases {
		src := release(m, selfupdate.SignManifest(priv, m))
		u := updater(t, src, pub, "v0.40.0")
		offer, err := u.Check(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		src.files[setupPath] = body
		var got bytes.Buffer
		err = u.Download(t.Context(), offer, &got)
		if err == nil {
			t.Errorf("%s: accepted %d bytes", name, got.Len())
		}
		if len(body) <= len(setup) && !errors.Is(err, domain.ErrReleaseAssetMismatch) {
			t.Errorf("%s: %v, want ErrReleaseAssetMismatch", name, err)
		}
	}
}

func TestDownloadWithoutOffer(t *testing.T) {
	pub, _ := key(t)
	src := release(nil, nil)
	if err := updater(t, src, pub, "v0.40.0").Download(t.Context(), selfupdate.Offer{}, io.Discard); err == nil || len(src.asked) != 0 {
		t.Errorf("zero offer: %v, asked %v", err, src.asked)
	}
}

func TestCheckSourceDown(t *testing.T) {
	pub, _ := key(t)
	boom := errors.New("github down")
	src := &fakeReleases{err: boom}
	if offer, err := updater(t, src, pub, "v0.40.0").Check(t.Context()); !errors.Is(err, boom) || offer.Available() {
		t.Errorf("available %v, %v", offer.Available(), err)
	}
}

// Просроченный манифест не верится, даже с верной подписью: старый подписанный
// манифест не «замораживает» окно на прежнем выпуске (ревью #679, п.6).
func TestCheckRefusesExpiredManifest(t *testing.T) {
	pub, priv := key(t)
	m := manifestExpiring("v0.41.0", setup, "2026-11-20T00:00:00Z")
	u := updater(t, release(m, selfupdate.SignManifest(priv, m)), pub, "v0.40.0")
	u.Now = func() time.Time { return time.Date(2026, 11, 19, 23, 59, 0, 0, time.UTC) }
	if o, err := u.Check(t.Context()); err != nil || !o.Available() {
		t.Fatalf("before the expiry: %v %v", o.Available(), err)
	}
	u.Now = func() time.Time { return time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC) }
	if _, err := u.Check(t.Context()); !errors.Is(err, domain.ErrReleaseExpired) {
		t.Errorf("at the expiry: %v", err)
	}
}

func manifestExpiring(version string, body []byte, expires string) []byte {
	s := sha256.Sum256(body)
	return fmt.Appendf(nil, `{"version":%q,"expires":%q,"assets":[{"platform":"windows-amd64","name":"DVK-Setup.exe","sha256":%q,"size":%d}]}`,
		version, expires, hex.EncodeToString(s[:]), len(body))
}

// Срок дальше года окно не верит: подпись с датой в 9999 годе сводила бы защиту
// от заморозки на нет (ревью безопасности #681, п.2).
func TestCheckRefusesAbsurdExpiry(t *testing.T) {
	pub, priv := key(t)
	m := manifestExpiring("v0.41.0", setup, "9999-01-01T00:00:00Z")
	u := updater(t, release(m, selfupdate.SignManifest(priv, m)), pub, "v0.40.0")
	if _, err := u.Check(t.Context()); !errors.Is(err, domain.ErrBadReleaseManifest) {
		t.Errorf("%v", err)
	}
	ok := manifestExpiring("v0.41.0", setup, time.Now().AddDate(0, 0, 300).UTC().Format(time.RFC3339))
	u = updater(t, release(ok, selfupdate.SignManifest(priv, ok)), pub, "v0.40.0")
	if o, err := u.Check(t.Context()); err != nil || !o.Available() {
		t.Errorf("300 days: %v %v", o.Available(), err)
	}
}
