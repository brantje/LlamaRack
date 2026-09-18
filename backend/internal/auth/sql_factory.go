package auth

import (
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
)

// New is the compatibility/composition helper for the SQL-backed adapters.
func New(db database.Store, sessionLifetime time.Duration) *Service {
	return NewWithStores(Stores{
		Users: NewUserStore(db), Sessions: NewSessionStore(db), APIKeys: NewAPIKeyStore(db),
		ServiceAccounts: NewServiceAccountStore(db), OIDC: NewOIDCStore(db),
	}, sessionLifetime)
}
