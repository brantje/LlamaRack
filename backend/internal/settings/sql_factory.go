package settings

import "github.com/brantje/llamarack/backend/internal/database"

// New is the compatibility factory for the SQL-backed settings adapter.
func New(db database.Store, defaults Defaults) *Service {
	return NewWithStore(NewSettingStore(db), defaults)
}
