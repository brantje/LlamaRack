package auth

import (
	"context"
	"errors"

	"github.com/brantje/llamarack/backend/internal/database"
	"strings"
)

// dummyPasswordHash is a fixed Argon2id hash using the same parameters as
// newly-created user passwords. It is intentionally not a secret and must not
// be regenerated per login request.
const dummyPasswordHash = "argon2id$v=19$m=65536,t=3,p=2$bGxhbWFyYWNrLWR1bW15IQ$a6Cr0DiCWqUX8furaAqzoBPmehriMwC0QbbXDWi5QyQ"

func (s *Service) verifyLoginCredentials(ctx context.Context, work *passwordWorkReservation, username, password string) (User, string, error) {
	user, hash, enabled, queryErr := s.sessions.Credentials(ctx, strings.TrimSpace(username))
	if queryErr != nil && !errors.Is(queryErr, database.ErrNotFound) {
		return User{}, "", queryErr
	}

	verificationHash := dummyPasswordHash
	realAccount := queryErr == nil && enabled
	if realAccount {
		verificationHash = hash
	}
	verified, err := verifyPasswordWithReservation(ctx, work, password, verificationHash)
	if err != nil {
		return User{}, "", err
	}
	if !realAccount || !verified {
		return User{}, "", ErrInvalidCredentials
	}

	return user, hash, nil
}
