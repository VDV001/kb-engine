package blobs3

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
)

const YandexEndpoint = "https://storage.yandexcloud.net"

type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type StaticToken string

func (t StaticToken) Token(context.Context) (string, error) { return string(t), nil }

type MetadataToken struct {
	BaseURL string
	Client  *http.Client
	Now     func() time.Time
}

func (*MetadataToken) Token(context.Context) (string, error) {
	return "", errors.New("not implemented")
}

type Config struct {
	Endpoint string
	Bucket   string
	Prefix   string
	Token    TokenSource
	Client   *http.Client
}

type Store struct{}

func New(Config) (*Store, error) { return &Store{}, nil }

func (*Store) Get(context.Context, string) (blob.Object, error) {
	return blob.Object{}, errors.New("not implemented")
}

func (*Store) Put(context.Context, string, []byte, string) (string, error) {
	return "", errors.New("not implemented")
}
