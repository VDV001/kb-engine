package blob_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/blob/blobtest"
)

func TestValidPurgePrefix(t *testing.T) {
	for _, prefix := range []string{"users/a/", "users/a-b/", "users/a1/", "users/a-1-b/", "mailfiles/users/a/", "auth/seen/a.", "auth/seen/a-b."} {
		if err := blob.ValidPurgePrefix(prefix); err != nil {
			t.Errorf("ValidPurgePrefix(%q) = %v, want nil", prefix, err)
		}
	}
	for _, prefix := range blobtest.InvalidPurgePrefixes {
		if err := blob.ValidPurgePrefix(prefix); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("ValidPurgePrefix(%q) = %v, want ErrInvalidKey", prefix, err)
		}
	}
}
