package main

import (
	"context"

	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// chatVersions gives the chat the families of the «Артефакты» tab: те же
// списки, подписи «что нового», заметки и значки; chat и вкладка читают одно
// и то же, каждый не считает свое (#547).
type chatVersions struct{ d *versionsDesk }

// versionsAccess returns the versions of documents for the model, nil when the window has none.
func (d *chatDesk) versionsAccess() chat.Versions {
	if d.versions == nil {
		return nil
	}
	return chatVersions{d.versions}
}

// Families returns the families matching q with the notes and badges of the person.
func (c chatVersions) Families(ctx context.Context, q string) ([]chat.VersionFamily, error) {
	fams, err := c.d.Families(ctx, q, false)
	if err != nil {
		return nil, err
	}
	out := make([]chat.VersionFamily, 0, len(fams))
	for _, f := range fams {
		cf := chat.VersionFamily{Name: f.Name}
		for _, v := range f.Versions {
			cf.Versions = append(cf.Versions, chat.VersionInfo{Path: v.Path, Date: v.Date, Size: v.Size, Note: v.Note, Mark: v.Mark, Current: v.Current, Dups: v.Dups})
		}
		out = append(out, cf)
	}
	return out, nil
}

// Text returns the text of a version of the last list.
func (c chatVersions) Text(ctx context.Context, path string) (string, error) {
	return c.d.Text(ctx, path)
}
