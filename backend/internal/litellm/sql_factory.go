package litellm

import (
	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/settings"
)

// New is the compatibility factory for the SQL-backed LiteLLM adapter.
func New(db database.Store, authService *auth.Service, secrets *huggingface.SecretStore, managerSettings *settings.Service) *Service {
	return NewWithStore(NewLiteLLMStore(db), authService, secrets, managerSettings)
}
