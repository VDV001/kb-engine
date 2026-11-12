package chat

import "context"

// Versions — итерации документов для режима «модель ищет» (#547): те же
// семейства, подписи и отметки, что на вкладке «Артефакты».
type Versions interface {
	// Families — семейства по запросу, версии в каждом от старой к новой.
	Families(ctx context.Context, q string) ([]VersionFamily, error)
	// Text — текст версии по пути из Families.
	Text(ctx context.Context, path string) (string, error)
}

// VersionFamily — документ и его версии.
type VersionFamily struct {
	Name     string
	Versions []VersionInfo
}

// VersionInfo — одна версия: что нового и что отметил человек.
type VersionInfo struct {
	Path    string
	Date    string // 02.01.2006
	Size    int64
	Note    string // что нового относительно предыдущей
	Mark    string // заметка человека
	Current bool   // значок «актуальная»
	Dups    []string
}
