package main

import (
	"context"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
)

// versionsAgent gives the agent the versions of documents of the window folders.
type versionsAgent struct{}

func newVersionsAgent(x *fileIndex) *versionsAgent { return &versionsAgent{} }

func (a *versionsAgent) Families(ctx context.Context, q string) ([]mcpserver.VersionFamily, error) {
	return nil, nil
}

func (a *versionsAgent) Text(ctx context.Context, path string) (string, error) { return "", nil }
