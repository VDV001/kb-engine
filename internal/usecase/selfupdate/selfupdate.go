package selfupdate

import (
	"context"
	"crypto/ed25519"

	"github.com/daniil/kb-engine/internal/domain"
)

type Source interface {
	Fetch(ctx context.Context, path string, limit int64) ([]byte, error)
}

type Updater struct {
	Source    Source
	PublicKey ed25519.PublicKey
	Current   domain.ReleaseVersion
	Platform  string
}

type Offer struct {
	version domain.ReleaseVersion
	asset   domain.ReleaseAsset
}

func (o Offer) Available() bool                { return false }
func (o Offer) Version() domain.ReleaseVersion { return o.version }

func SignManifest(ed25519.PrivateKey, []byte) []byte { return make([]byte, ed25519.SignatureSize) }

func (u Updater) Check(context.Context) (Offer, error) { return Offer{}, nil }

func (u Updater) Download(context.Context, Offer) ([]byte, error) { return nil, nil }
