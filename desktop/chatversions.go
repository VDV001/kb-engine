package main

import (
	"context"

	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// chatVersions gives the chat the families of the «Артефакты» tab.
type chatVersions struct{ d *versionsDesk }

// versionsAccess returns the versions of documents for the model, nil when the window has none.
func (d *chatDesk) versionsAccess() chat.Versions { return nil }

// Families returns the families matching q with the notes and badges of the person.
func (c chatVersions) Families(ctx context.Context, q string) ([]chat.VersionFamily, error) {
	return nil, nil
}

// Text returns the text of a version of the last list.
func (c chatVersions) Text(ctx context.Context, path string) (string, error) { return "", nil }
