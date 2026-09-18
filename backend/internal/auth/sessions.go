package auth

import (
	"context"
	"database/sql"

	"github.com/brantje/llamarack/backend/internal/database"
	"strings"
	"time"
)

func (s *Service) LoginWithMetadata(ctx context.Context, username, password, remoteAddress, userAgent string) (string, string, User, error) {
	work, err := reservePasswordWork()
	if err != nil {
		return "", "", User{}, err
	}
	defer work.Release()

	user, hash, err := s.verifyLoginCredentials(ctx, work, username, password)
	if err != nil {
		return "", "", User{}, err
	}
	var rehashed string
	if passwordNeedsRehash(hash) {
		rehashed, err = hashPasswordWithReservation(ctx, work, password)
		if err != nil {
			return "", "", User{}, err
		}
	}
	work.Release()

	token, err := randomToken(32)
	if err != nil {
		return "", "", User{}, err
	}
	csrf, err := randomToken(24)
	if err != nil {
		return "", "", User{}, err
	}
	id, err := randomToken(16)
	if err != nil {
		return "", "", User{}, err
	}
	now := time.Now()
	session := &sessionCreate{
		ID: id, UserID: user.ID, TokenHash: tokenHash(token), CSRFTokenHash: tokenHash(csrf),
		CreatedAt: now.Unix(), ExpiresAt: now.Add(s.SessionLifetime()).Unix(),
		RemoteAddress: strings.TrimSpace(remoteAddress), UserAgent: truncate(userAgent, 512),
	}
	if err := s.sessions.CommitLogin(ctx, user.ID, hash, rehashed, now.Unix(), session); err != nil {
		return "", "", User{}, err
	}
	last := now.Unix()
	user.LastLoginAt = &last
	return token, csrf, user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	return s.sessions.DeleteByTokenHash(ctx, tokenHash(token))
}

func (s *Service) SessionUserWithSession(ctx context.Context, token string) (User, Session, error) {
	user, session, err := s.sessions.ResolveByTokenHash(ctx, tokenHash(token), time.Now().Unix())
	if err != nil {
		return User{}, Session{}, ErrSessionInvalid
	}
	session.Current = true
	return user, session, nil
}

func (s *Service) ValidateCSRF(ctx context.Context, sessionToken, csrfToken string) error {
	if sessionToken == "" || csrfToken == "" {
		return ErrCSRFInvalid
	}
	ok, err := s.sessions.ValidateCSRF(ctx, tokenHash(sessionToken), tokenHash(csrfToken), time.Now().Unix())
	if err != nil || !ok {
		return ErrCSRFInvalid
	}
	return nil
}

func (s *Service) ListSessions(ctx context.Context, userID int64, currentSessionID string) ([]Session, error) {
	items, err := s.sessions.List(ctx, userID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Current = currentSessionID != "" && items[i].ID == currentSessionID
	}
	return items, nil
}

func (s *Service) RevokeSession(ctx context.Context, id string) error {
	return s.sessions.Revoke(ctx, id)
}

func (s *Service) RevokeOwnSession(ctx context.Context, userID int64, id string) error {
	id = strings.TrimSpace(id)
	if id == "" || userID <= 0 {
		return database.ClassifyError(sql.ErrNoRows)
	}
	return s.sessions.RevokeOwn(ctx, userID, id)
}

func (s *Service) RevokeOtherSessions(ctx context.Context, userID int64, keepSessionID string) (int64, error) {
	return s.sessions.RevokeOther(ctx, userID, keepSessionID)
}

func (s *Service) RevokeAllSessions(ctx context.Context, userID int64) (int64, error) {
	return s.sessions.RevokeAll(ctx, userID)
}
