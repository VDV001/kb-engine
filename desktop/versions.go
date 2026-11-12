package main

import (
	"github.com/daniil/kb-engine/internal/adapter/localfiles"
	"github.com/daniil/kb-engine/internal/adapter/versionbook"
)

// Вкладка «Артефакты» (#547): документы папок окна собраны в семейства, у каждой
// версии подпись «что нового». Сама сборка живет в versionbook: ею пользуются
// и чат, и MCP, чтобы все видели одно и то же.
type (
	versionsDesk = versionbook.Desk
	familyView   = versionbook.FamilyView
	compareView  = versionbook.CompareView
	compareLine  = versionbook.CompareLine
)

func newVersionsDesk(source func() *localfiles.Source, marksPath string) *versionsDesk {
	return versionbook.New(source, marksPath)
}
