package domain_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

func TestNewReleaseVersion(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"v0.41.0", "v0.41.0", true},
		{"v1.2.30", "v1.2.30", true},
		// git describe между тегами: сборка считается версией последнего тега,
		// и выпуск с тем же номером обновлением не предлагается.
		{"v0.40.0-11-g154839a6", "v0.40.0", true},
		// --dirty: сборка из дерева с правками — все равно версия тега.
		{"v0.40.0-11-g154839a6-dirty", "v0.40.0", true},
		{"v0.40.0-dirty", "v0.40.0", true},
		{"v0.40.0-dirty-dirty", "", false},
		{"dev", "", false},
		{"", "", false},
		{"0.41.0", "", false},
		{"v0.41", "", false},
		{"v0.41.0.1", "", false},
		{"v01.2.3", "", false},
		{"v-1.2.3", "", false},
		{"v1.2.3-rc1", "", false},
		{"154839a6", "", false},
	}
	for _, c := range cases {
		v, err := domain.NewReleaseVersion(c.in)
		if c.ok != (err == nil) {
			t.Errorf("%q: err %v", c.in, err)
			continue
		}
		if !c.ok {
			if !errors.Is(err, domain.ErrBadReleaseVersion) {
				t.Errorf("%q: %v, want ErrBadReleaseVersion", c.in, err)
			}
			continue
		}
		if v.String() != c.want {
			t.Errorf("%q: %s, want %s", c.in, v, c.want)
		}
	}
}

func TestReleaseVersionNewer(t *testing.T) {
	cases := []struct {
		a, b  string
		newer bool
	}{
		{"v0.41.0", "v0.40.0", true},
		{"v0.40.0", "v0.41.0", false},
		{"v0.40.0", "v0.40.0", false},
		{"v0.40.10", "v0.40.9", true}, // числа, а не строки
		{"v1.0.0", "v0.99.99", true},
		{"v0.40.1", "v0.40.0-11-g154839a6", true},
		{"v0.40.0", "v0.40.0-11-g154839a6", false},
	}
	for _, c := range cases {
		a, _ := domain.NewReleaseVersion(c.a)
		b, _ := domain.NewReleaseVersion(c.b)
		if got := a.Newer(b); got != c.newer {
			t.Errorf("%s newer than %s: %v, want %v", c.a, c.b, got, c.newer)
		}
	}
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func manifestJSON(version string, assets ...string) []byte {
	return fmt.Appendf(nil, `{"version":%q,"expires":"2099-01-01T00:00:00Z","assets":[%s]}`, version, strings.Join(assets, ","))
}

func assetJSON(platform, name string, body []byte) string {
	return fmt.Sprintf(`{"platform":%q,"name":%q,"sha256":%q,"size":%d}`, platform, name, sum(body), len(body))
}

func TestParseReleaseManifest(t *testing.T) {
	mac, win := []byte("mac zip"), []byte("windows setup")
	good := manifestJSON("v0.41.0", assetJSON("darwin-arm64", "DVK-mac-arm64.zip", mac), assetJSON("windows-amd64", "DVK-Setup.exe", win))
	m, err := domain.ParseReleaseManifest(good)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version().String() != "v0.41.0" {
		t.Errorf("version %s", m.Version())
	}
	a, ok := m.Asset("windows-amd64")
	if !ok || a.Name() != "DVK-Setup.exe" || a.Size() != int64(len(win)) {
		t.Fatalf("windows asset %+v %v", a, ok)
	}
	if _, ok := m.Asset("linux-amd64"); ok {
		t.Error("asset for a platform the manifest does not name")
	}

	bad := map[string][]byte{
		"not json":           []byte("{"),
		"unknown field":      []byte(`{"version":"v0.41.0","assets":[],"url":"https://evil"}`),
		"describe version":   manifestJSON("v0.41.0-1-gabc", assetJSON("darwin-arm64", "a.zip", mac)),
		"no assets":          manifestJSON("v0.41.0"),
		"path in name":       manifestJSON("v0.41.0", assetJSON("darwin-arm64", "../kb.app.zip", mac)),
		"slash in name":      manifestJSON("v0.41.0", assetJSON("darwin-arm64", "x/a.zip", mac)),
		"dot name":           manifestJSON("v0.41.0", assetJSON("darwin-arm64", "..", mac)),
		"unknown platform":   manifestJSON("v0.41.0", assetJSON("plan9-386", "a.zip", mac)),
		"duplicate platform": manifestJSON("v0.41.0", assetJSON("darwin-arm64", "a.zip", mac), assetJSON("darwin-arm64", "b.zip", win)),
		// На странице выпуска имя одно на файл: второй затер бы первый.
		"duplicate name": manifestJSON("v0.41.0", assetJSON("darwin-arm64", "DVK.zip", mac), assetJSON("windows-amd64", "DVK.zip", win)),
		"short sha":      manifestJSON("v0.41.0", `{"platform":"darwin-arm64","name":"a.zip","sha256":"abcd","size":1}`),
		"zero size":      manifestJSON("v0.41.0", `{"platform":"darwin-arm64","name":"a.zip","sha256":"`+sum(nil)+`","size":0}`),
		"huge size":      manifestJSON("v0.41.0", fmt.Sprintf(`{"platform":"darwin-arm64","name":"a.zip","sha256":"%s","size":%d}`, sum(nil), domain.MaxReleaseAsset+1)),
		"trailing data":  append(good, []byte(` {}`)...),
	}
	for name, b := range bad {
		if _, err := domain.ParseReleaseManifest(b); !errors.Is(err, domain.ErrBadReleaseManifest) {
			t.Errorf("%s: %v, want ErrBadReleaseManifest", name, err)
		}
	}
}

// Подмененный файл: имя и размер те же, содержимое другое — отказ.
func TestReleaseAssetMatches(t *testing.T) {
	body := []byte("windows setup")
	m, err := domain.ParseReleaseManifest(manifestJSON("v0.41.0", assetJSON("windows-amd64", "DVK-Setup.exe", body)))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := m.Asset("windows-amd64")
	if err := a.Matches(body); err != nil {
		t.Errorf("the published file: %v", err)
	}
	for name, b := range map[string][]byte{
		"substituted": []byte("windows sEtup"),
		"truncated":   body[:5],
		"longer":      append(append([]byte(nil), body...), 0),
		"empty":       nil,
	} {
		if err := a.Matches(b); !errors.Is(err, domain.ErrReleaseAssetMismatch) {
			t.Errorf("%s: %v, want ErrReleaseAssetMismatch", name, err)
		}
	}
}

// Манифест, собранный при выпуске, читается окном тем же разбором.
func TestNewReleaseManifestRoundTrip(t *testing.T) {
	mac, win := []byte("mac zip"), []byte("windows setup")
	a1, err := domain.NewReleaseAsset("darwin-arm64", "DVK-mac-arm64.zip", mac)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := domain.NewReleaseAsset("windows-amd64", "DVK-Setup.exe", win)
	if err != nil {
		t.Fatal(err)
	}
	m, err := domain.NewReleaseManifest("v0.41.0", time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), a1, a2)
	if err != nil {
		t.Fatal(err)
	}
	back, err := domain.ParseReleaseManifest(m.JSON())
	if err != nil {
		t.Fatalf("%s: %v", m.JSON(), err)
	}
	if back.Version().String() != "v0.41.0" {
		t.Errorf("version %s", back.Version())
	}
	for platform, body := range map[string][]byte{"darwin-arm64": mac, "windows-amd64": win} {
		a, ok := back.Asset(platform)
		if !ok || a.Matches(body) != nil {
			t.Errorf("%s: %v %v", platform, ok, a.Matches(body))
		}
	}
}

func TestNewReleaseManifestRefusals(t *testing.T) {
	body := []byte("x")
	for name, c := range map[string]struct{ platform, file string }{
		"path in name":     {"darwin-arm64", "../a.zip"},
		"unknown platform": {"linux-amd64", "a.zip"},
	} {
		if _, err := domain.NewReleaseAsset(c.platform, c.file, body); !errors.Is(err, domain.ErrBadReleaseManifest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := domain.NewReleaseAsset("darwin-arm64", "a.zip", nil); !errors.Is(err, domain.ErrBadReleaseManifest) {
		t.Errorf("empty file: %v", err)
	}
	a, _ := domain.NewReleaseAsset("darwin-arm64", "a.zip", body)
	b, _ := domain.NewReleaseAsset("darwin-arm64", "b.zip", body)
	for name, c := range map[string]struct {
		version string
		assets  []domain.ReleaseAsset
	}{
		"describe version":   {"v0.41.0-1-gabc", []domain.ReleaseAsset{a}},
		"no assets":          {"v0.41.0", nil},
		"duplicate platform": {"v0.41.0", []domain.ReleaseAsset{a, b}},
	} {
		if _, err := domain.NewReleaseManifest(c.version, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), c.assets...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Манифест несет срок годности: без него или с негодным — отказ; подпись
// навсегда дала бы «заморозку» на старом выпуске (ревью #679, п.6).
func TestParseReleaseManifestExpires(t *testing.T) {
	a := assetJSON("darwin-arm64", "DVK-mac-arm64.zip", []byte("mac"))
	with := func(exp string) []byte {
		return fmt.Appendf(nil, `{"version":"v0.41.0","expires":%s,"assets":[%s]}`, exp, a)
	}
	for name, b := range map[string][]byte{
		"missing":    fmt.Appendf(nil, `{"version":"v0.41.0","assets":[%s]}`, a),
		"not a date": with(`"завтра"`),
		"date only":  with(`"2027-01-01"`),
		"number":     with(`1790000000`),
	} {
		if _, err := domain.ParseReleaseManifest(b); !errors.Is(err, domain.ErrBadReleaseManifest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	m, err := domain.ParseReleaseManifest(with(`"2027-01-01T00:00:00+03:00"`))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 12, 31, 21, 0, 0, 0, time.UTC); !m.Expires().Equal(want) {
		t.Errorf("expires %v, want %v", m.Expires(), want)
	}
}
