package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
)

// Отказы самообновления окна DVK (#651).
var (
	ErrBadReleaseVersion    = errors.New("release version is not vX.Y.Z")
	ErrBadReleaseManifest   = errors.New("release manifest is not valid")
	ErrReleaseSignature     = errors.New("release manifest signature is not valid")
	ErrReleaseAssetMismatch = errors.New("release file does not match the manifest")
	ErrNoReleaseAsset       = errors.New("release has no file for this platform")
)

// MaxReleaseAsset — потолок файла выпуска: kb.app и установщик весят десятки
// мегабайт; файл больше — не наш, и окно не станет держать его в памяти.
const MaxReleaseAsset = 512 << 20

// ReleaseVersion is the window's version, vX.Y.Z (#651). Сборка между тегами
// (git describe: v0.40.0-11-g154839a6) считается версией последнего тега:
// выпуск с тем же номером ей не предлагается, следующий — предлагается.
type ReleaseVersion struct{ major, minor, patch int }

var (
	releaseTag      = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	releaseDescribe = regexp.MustCompile(`^(v[0-9.]+)-[0-9]+-g[0-9a-f]+$`)
)

// NewReleaseVersion reads a tag or a git describe of the build.
func NewReleaseVersion(s string) (ReleaseVersion, error) {
	if m := releaseDescribe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	return parseReleaseTag(s)
}

// parseReleaseTag accepts only a bare tag: манифест выпуска называет тег, а не
// сборку между тегами.
func parseReleaseTag(s string) (ReleaseVersion, error) {
	m := releaseTag.FindStringSubmatch(s)
	if m == nil {
		return ReleaseVersion{}, fmt.Errorf("%w: %q", ErrBadReleaseVersion, s)
	}
	var n [3]int
	for i, part := range m[1:] {
		v, err := strconv.Atoi(part)
		if err != nil { // переполнение int
			return ReleaseVersion{}, fmt.Errorf("%w: %q", ErrBadReleaseVersion, s)
		}
		n[i] = v
	}
	return ReleaseVersion{n[0], n[1], n[2]}, nil
}

// Newer reports whether v is a later release than o.
func (v ReleaseVersion) Newer(o ReleaseVersion) bool {
	if v.major != o.major {
		return v.major > o.major
	}
	if v.minor != o.minor {
		return v.minor > o.minor
	}
	return v.patch > o.patch
}

func (v ReleaseVersion) String() string { return fmt.Sprintf("v%d.%d.%d", v.major, v.minor, v.patch) }

// ReleaseAsset is one file of a release: kb.app в архиве для Mac или
// установщик для Windows, с размером и sha256 из подписанного манифеста.
type ReleaseAsset struct {
	platform, name string
	sha256         [sha256.Size]byte
	size           int64
}

// Name is the bare file name on the release.
func (a ReleaseAsset) Name() string { return a.name }

// Size is the file's length in bytes, по манифесту.
func (a ReleaseAsset) Size() int64 { return a.size }

// Matches checks the downloaded bytes against the manifest: подмененный файл
// того же имени и размера отвергается по sha256.
func (a ReleaseAsset) Matches(b []byte) error {
	if int64(len(b)) != a.size || sha256.Sum256(b) != a.sha256 {
		return fmt.Errorf("%w: %s", ErrReleaseAssetMismatch, a.name)
	}
	return nil
}

// ReleaseManifest is what the owner signs at release (#651): версия и файлы по
// платформам. Окно верит только ему, а не странице выпуска GitHub.
type ReleaseManifest struct {
	version ReleaseVersion
	assets  []ReleaseAsset
}

// releasePlatforms — платформы, под которые собирается окно.
var releasePlatforms = []string{"darwin-arm64", "windows-amd64"}

// releaseAssetName — голое имя файла: из него окно строит адрес на GitHub, и
// «../» или «/» увели бы загрузку на чужой путь.
var releaseAssetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type releaseAssetJSON struct {
	Platform string `json:"platform"`
	Name     string `json:"name"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

type releaseManifestJSON struct {
	Version string             `json:"version"`
	Assets  []releaseAssetJSON `json:"assets"`
}

// ParseReleaseManifest reads the manifest; звать после проверки подписи.
func ParseReleaseManifest(b []byte) (ReleaseManifest, error) {
	bad := func(why string) (ReleaseManifest, error) {
		return ReleaseManifest{}, fmt.Errorf("%w: %s", ErrBadReleaseManifest, why)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var raw releaseManifestJSON
	if err := dec.Decode(&raw); err != nil {
		return bad(err.Error())
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return bad("data after the manifest")
	}
	v, err := parseReleaseTag(raw.Version)
	if err != nil {
		return bad(err.Error())
	}
	if len(raw.Assets) == 0 {
		return bad("no files")
	}
	m := ReleaseManifest{version: v}
	for _, r := range raw.Assets {
		if _, dup := m.Asset(r.Platform); dup {
			return bad("two files for " + r.Platform)
		}
		a, err := parseReleaseAsset(r)
		if err != nil {
			return bad(err.Error())
		}
		m.assets = append(m.assets, a)
	}
	return m, nil
}

func parseReleaseAsset(r releaseAssetJSON) (ReleaseAsset, error) {
	if !slices.Contains(releasePlatforms, r.Platform) {
		return ReleaseAsset{}, fmt.Errorf("unknown platform %q", r.Platform)
	}
	if !releaseAssetName.MatchString(r.Name) {
		return ReleaseAsset{}, fmt.Errorf("file name %q", r.Name)
	}
	if r.Size <= 0 || r.Size > MaxReleaseAsset {
		return ReleaseAsset{}, fmt.Errorf("size %d of %s", r.Size, r.Name)
	}
	sum, err := hex.DecodeString(r.SHA256)
	if err != nil || len(sum) != sha256.Size {
		return ReleaseAsset{}, fmt.Errorf("sha256 of %s", r.Name)
	}
	a := ReleaseAsset{platform: r.Platform, name: r.Name, size: r.Size}
	copy(a.sha256[:], sum)
	return a, nil
}

// Version is the release's tag.
func (m ReleaseManifest) Version() ReleaseVersion { return m.version }

// Asset is the file for the platform, if the release has one.
func (m ReleaseManifest) Asset(platform string) (ReleaseAsset, bool) {
	for _, a := range m.assets {
		if a.platform == platform {
			return a, true
		}
	}
	return ReleaseAsset{}, false
}

// NewReleaseAsset describes a built file for the manifest, по тем же правилам,
// по которым окно его прочтет.
func NewReleaseAsset(platform, name string, body []byte) (ReleaseAsset, error) {
	s := sha256.Sum256(body)
	a, err := parseReleaseAsset(releaseAssetJSON{Platform: platform, Name: name, SHA256: hex.EncodeToString(s[:]), Size: int64(len(body))})
	if err != nil {
		return ReleaseAsset{}, fmt.Errorf("%w: %w", ErrBadReleaseManifest, err)
	}
	return a, nil
}

// NewReleaseManifest builds the manifest the release signs.
func NewReleaseManifest(version string, assets ...ReleaseAsset) (ReleaseManifest, error) {
	m, err := ParseReleaseManifest(manifestJSON(version, assets))
	if err != nil {
		return ReleaseManifest{}, err
	}
	return m, nil
}

// JSON is the manifest as published: эти байты и подписываются.
func (m ReleaseManifest) JSON() []byte { return manifestJSON(m.version.String(), m.assets) }

func manifestJSON(version string, assets []ReleaseAsset) []byte {
	raw := releaseManifestJSON{Version: version, Assets: []releaseAssetJSON{}}
	for _, a := range assets {
		raw.Assets = append(raw.Assets, releaseAssetJSON{Platform: a.platform, Name: a.name, SHA256: hex.EncodeToString(a.sha256[:]), Size: a.size})
	}
	b, _ := json.MarshalIndent(raw, "", "  ") // строки и числа: ошибки не бывает
	return append(b, '\n')
}
