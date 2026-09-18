package downloads

import (
	"context"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/huggingface"
)

// New is the compatibility factory for the SQL-backed download adapter.
func New(ctx context.Context, db database.Store, modelsDir string, hf *huggingface.Client, limits ...SizeLimitFunc) *Manager {
	return NewWithStore(ctx, NewDownloadStore(db), modelsDir, hf, limits...)
}
