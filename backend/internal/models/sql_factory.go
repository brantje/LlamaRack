package models

import "github.com/brantje/llamarack/backend/internal/database"

// New is the compatibility factory for the SQL-backed model adapter.
func New(db database.Store, modelsDir string) *Service {
	return NewWithStore(NewModelStore(db), modelsDir)
}
