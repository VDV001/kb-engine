package domain

import "errors"

var (
	ErrBadReleaseVersion    = errors.New("release version is not vX.Y.Z")
	ErrBadReleaseManifest   = errors.New("release manifest is not valid")
	ErrReleaseSignature     = errors.New("release manifest signature is not valid")
	ErrReleaseAssetMismatch = errors.New("release file does not match the manifest")
	ErrNoReleaseAsset       = errors.New("release has no file for this platform")
)

const MaxReleaseAsset = 512 << 20

type ReleaseVersion struct{ major, minor, patch int }

func NewReleaseVersion(string) (ReleaseVersion, error) { return ReleaseVersion{}, nil }

func (v ReleaseVersion) Newer(ReleaseVersion) bool { return false }

func (v ReleaseVersion) String() string { return "" }

type ReleaseAsset struct {
	platform, name string
	sha256         [32]byte
	size           int64
}

func (a ReleaseAsset) Platform() string { return a.platform }
func (a ReleaseAsset) Name() string     { return a.name }
func (a ReleaseAsset) Size() int64      { return a.size }

func (a ReleaseAsset) Matches([]byte) error { return nil }

type ReleaseManifest struct {
	version ReleaseVersion
	assets  []ReleaseAsset
}

func ParseReleaseManifest([]byte) (ReleaseManifest, error) { return ReleaseManifest{}, nil }

func (m ReleaseManifest) Version() ReleaseVersion { return m.version }

func (m ReleaseManifest) Asset(string) (ReleaseAsset, bool) { return ReleaseAsset{}, false }
