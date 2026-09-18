package instances

import "github.com/brantje/llamarack/backend/internal/database"

// New is the compatibility factory for the SQL-backed instance adapter.
func New(db database.Store) *Service {
	return NewWithStore(NewInstanceStore(db))
}
