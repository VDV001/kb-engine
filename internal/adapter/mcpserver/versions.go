package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// VersionFinder gives the agent the iterations of documents (#547): те же
// семейства, подписи «что нового» и заметки, что на вкладке «Артефакты» окна.
// Только чтение: заметки и значки агент не ставит.
type VersionFinder interface {
	// Families — семейства по запросу, версии в каждом от старой к новой.
	Families(ctx context.Context, q string) ([]VersionFamily, error)
	// Text — текст версии по пути из Families.
	Text(ctx context.Context, path string) (string, error)
}

// VersionFamily — документ и его версии.
type VersionFamily struct {
	Name     string
	Versions []VersionEntry
}

// VersionEntry — одна версия.
type VersionEntry struct {
	Path, Date, Note, Mark string
	Size                   int64
	Current                bool
}

func addVersionTools(s *mcp.Server, j journal, f VersionFinder) {}
