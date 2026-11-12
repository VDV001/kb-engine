package main

import (
	"context"
	"path/filepath"

	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/adapter/versionbook"
)

// versionsAgent gives the agent the versions of documents of the window folders:
// те же семейства, подписи и заметки, что на вкладке «Артефакты» (заметки окно
// пишет рядом с config.json). Агент только читает.
type versionsAgent struct{ desk *versionbook.Desk }

func newVersionsAgent(x *fileIndex) *versionsAgent {
	return &versionsAgent{desk: versionbook.New(x.versionsSource, filepath.Join(filepath.Dir(x.cfgPath), "version-marks.json"))}
}

// Families returns the families matching q.
func (a *versionsAgent) Families(ctx context.Context, q string) ([]mcpserver.VersionFamily, error) {
	fams, err := a.desk.Families(ctx, q, false)
	if err != nil {
		return nil, err
	}
	out := make([]mcpserver.VersionFamily, 0, len(fams))
	for _, f := range fams {
		mf := mcpserver.VersionFamily{Name: f.Name}
		for _, v := range f.Versions {
			mf.Versions = append(mf.Versions, mcpserver.VersionEntry{Path: v.Path, Date: v.Date, Note: v.Note, Mark: v.Mark, Size: v.Size, Current: v.Current})
		}
		out = append(out, mf)
	}
	return out, nil
}

// Text returns the text of a version of the last list.
func (a *versionsAgent) Text(ctx context.Context, path string) (string, error) {
	return a.desk.Text(ctx, path)
}
