package blob

import (
	"context"
	"errors"
	"io/fs"
)

func FS(context.Context, Store, string) (fs.FS, error) { return nil, errors.New("not implemented") }
