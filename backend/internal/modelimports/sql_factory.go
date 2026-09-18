package modelimports

import (
	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/models"
)

// New is the compatibility factory for SQL-backed import and instance adapters.
func New(db database.Store, modelsDir string, modelService *models.Service, downloadManager *downloads.Manager, starter InstanceStarter) *Service {
	return NewWithStores(NewStore(db), instances.NewWithStore(instances.NewInstanceStore(db)), modelsDir, modelService, downloadManager, starter)
}
