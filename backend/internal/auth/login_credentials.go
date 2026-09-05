package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// dummyPasswordHash is a fixed Argon2id hash using the same parameters as
// newly-created user passwords. It is intentionally not a secret and must not
// be regenerated per login request.
const dummyPasswordHash = "argon2id$v=19$m=65536,t=3,p=2$bGxhbWFyYWNrLWR1bW15IQ$a6Cr0DiCWqUX8furaAqzoBPmehriMwC0QbbXDWi5QyQ"

func (s *Service) verifyLoginCredentials(ctx context.Context, work *passwordWorkReservation, username, password string) (User, string, error) {
	var user User
	var hash string
	var enabled int
	var lastLogin sql.NullInt64
	queryErr := s.db.QueryRowContext(ctx, "SELECT id,username,password_hash,enabled,created_at,last_login_at FROM users WHERE username=?", strings.TrimSpace(username)).Scan(
		&user.ID,
		&user.Username,
		&hash,
		&enabled,
		&user.CreatedAt,
		&lastLogin,
	)
	if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
		return User{}, "", queryErr
	}

	verificationHash := dummyPasswordHash
	realAccount := queryErr == nil && enabled != 0
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

	user.Enabled = true
	if lastLogin.Valid {
		value := lastLogin.Int64
		user.LastLoginAt = &value
	}
	return user, hash, nil
}
