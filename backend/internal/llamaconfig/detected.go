package llamaconfig

import (
	"context"
	"database/sql"
	"sync"
)

type DetectedDefaultsProvider func(context.Context, string) (map[string]string, error)

var detectedDefaultsProviders sync.Map

// RegisterDetectedDefaultsProvider installs runtime defaults derived from the
// backing model file. Detected values are lower priority than every explicit
// global, model, or instance option.
func RegisterDetectedDefaultsProvider(db *sql.DB, provider DetectedDefaultsProvider) func() {
	if db == nil || provider == nil {
		return func() {}
	}
	detectedDefaultsProviders.Store(db, provider)
	return func() { detectedDefaultsProviders.Delete(db) }
}

func detectedDefaultsProvider(db *sql.DB) DetectedDefaultsProvider {
	value, ok := detectedDefaultsProviders.Load(db)
	if !ok {
		return nil
	}
	provider, _ := value.(DetectedDefaultsProvider)
	return provider
}
