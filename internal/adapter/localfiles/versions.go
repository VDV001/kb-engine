package localfiles

import (
	"context"

	"github.com/daniil/kb-engine/internal/usecase/versions"
)

// Versions возвращает документы под корнями (docx, pptx, xlsx, pdf) для
// сборки семейств версий.
func (s *Source) Versions(ctx context.Context) ([]versions.File, error) { return nil, nil }
