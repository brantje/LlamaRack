package observability

import "github.com/brantje/llamarack/backend/internal/database"

// New is the compatibility factory for the SQL-backed observability adapter.
func New(db database.Store) *Service {
	return NewWithStore(NewObservabilityStore(db))
}
