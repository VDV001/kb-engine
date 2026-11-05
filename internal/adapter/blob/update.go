package blob

import (
	"context"
	"errors"
)

func Update(context.Context, Store, string, func([]byte) ([]byte, error)) error {
	return errors.New("not implemented")
}
