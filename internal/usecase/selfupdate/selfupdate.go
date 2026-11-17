// Package selfupdate is the DVK window updating itself (#651): окно спрашивает
// GitHub Releases о манифесте выпуска, верит ему только с подписью ключа
// владельца и качает файл своей платформы, сверяя его с манифестом.
package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Source reads a release file by its path under the repository's releases
// (download/<тег>/<имя>): Fetch — целиком (манифест), Open — потоком (сборка);
// больше limit байт — отказ.
type Source interface {
	Fetch(ctx context.Context, path string, limit int64) ([]byte, error)
	Open(ctx context.Context, path string, limit int64) (io.ReadCloser, error)
}

const (
	// channel — постоянный выпуск, где лежит манифест последнего окна. Не
	// «latest»: выпуск только сервера стал бы latest, и без манифеста все окна
	// получили бы 404 (ревью #672). Файлы окна — в выпуске своего тега.
	channel      = "download/dvk-latest/"
	manifestName = "dvk-manifest.json"
	// maxManifest — манифест на две платформы весит сотни байт.
	maxManifest = 64 << 10
	// signedPrefix отделяет подпись манифеста от любой другой подписи тем же
	// ключом: подписанное для чего-то еще за манифест не сойдет.
	signedPrefix = "dvk-release-manifest-v1\n"
)

// ErrNoOffer — Download позван без предложения, полученного от Check.
var ErrNoOffer = errors.New("no update offer to download")

// Updater checks for and downloads a newer window.
type Updater struct {
	Source    Source
	PublicKey ed25519.PublicKey // вшит в окно; без него не верим ничему
	Current   domain.ReleaseVersion
	Platform  string           // darwin-arm64, windows-amd64
	Now       func() time.Time // nil — time.Now; срок годности манифеста сверяется с ним
}

// Offer is a newer release whose manifest passed the signature. Поля закрыты:
// предложение строит только Check, и Download не качает непроверенное.
type Offer struct {
	version domain.ReleaseVersion
	asset   domain.ReleaseAsset
	ok      bool
}

// Available reports whether there is a newer release to install.
func (o Offer) Available() bool { return o.ok }

// Version is the offered release's tag.
func (o Offer) Version() domain.ReleaseVersion { return o.version }

// AssetName is the offered file's name in the release.
func (o Offer) AssetName() string { return o.asset.Name() }

// SignManifest signs the manifest bytes as the release does.
func SignManifest(key ed25519.PrivateKey, manifest []byte) []byte {
	return ed25519.Sign(key, signed(manifest))
}

func signed(manifest []byte) []byte {
	return append([]byte(signedPrefix), manifest...)
}

// Check reads the latest release. Подпись проверяется до разбора манифеста;
// выпуск не новее текущей сборки — не обновление (откат запрещен).
func (u Updater) Check(ctx context.Context) (Offer, error) {
	manifest, err := u.Source.Fetch(ctx, channel+manifestName, maxManifest)
	if err != nil {
		return Offer{}, err
	}
	sig, err := u.Source.Fetch(ctx, channel+manifestName+".sig", ed25519.SignatureSize)
	if err != nil {
		return Offer{}, err
	}
	if len(u.PublicKey) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize ||
		!ed25519.Verify(u.PublicKey, signed(manifest), sig) {
		return Offer{}, domain.ErrReleaseSignature
	}
	m, err := domain.ParseReleaseManifest(manifest)
	if err != nil {
		return Offer{}, err
	}
	now := time.Now
	if u.Now != nil {
		now = u.Now
	}
	if m.Expired(now()) { // подпись верна, но манифест устарел: подсунуть старый нельзя
		return Offer{}, domain.ErrReleaseExpired
	}
	if !m.Version().Newer(u.Current) {
		return Offer{}, nil
	}
	a, ok := m.Asset(u.Platform)
	if !ok {
		return Offer{}, fmt.Errorf("%w: %s %s", domain.ErrNoReleaseAsset, m.Version(), u.Platform)
	}
	return Offer{version: m.Version(), asset: a, ok: true}, nil
}

// Download streams the offered file into dst and checks it against the signed
// manifest: размер и sha256 считаются по дороге (ревью #672: не в память).
// Ошибка — содержимое dst не годится: окно пишет во временный файл и стирает его.
func (u Updater) Download(ctx context.Context, o Offer, dst io.Writer) error {
	if !o.ok {
		return ErrNoOffer
	}
	rc, err := u.Source.Open(ctx, "download/"+o.version.String()+"/"+o.asset.Name(), o.asset.Size())
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), rc)
	if err != nil {
		return err
	}
	var sum [sha256.Size]byte
	h.Sum(sum[:0])
	return o.asset.MatchesSum(n, sum)
}
