package main

import (
	"context"

	"github.com/daniil/kb-engine/internal/adapter/localfiles"
)

// versionView — версия документа в ленте семейства.
type versionView struct {
	Path string   `json:"path"`
	Date string   `json:"date"`
	Size int64    `json:"size"`
	Note string   `json:"note"`
	Dups []string `json:"dups"`
}

// familyView — один документ и его итерации лентой по времени.
type familyView struct {
	Name     string        `json:"name"`
	Versions []versionView `json:"versions"`
}

type versionsDesk struct {
	source func() *localfiles.Source
}

func newVersionsDesk(source func() *localfiles.Source) *versionsDesk {
	return &versionsDesk{source: source}
}

// Families returns the document families under the source folders.
func (d *versionsDesk) Families(ctx context.Context, q string, fresh bool) ([]familyView, error) {
	return nil, nil
}
