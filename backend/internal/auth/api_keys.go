package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *Service) CreateAPIKeyForUser(ctx context.Context, name string, creatorUserID int64) (APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	secret, err := randomToken(32)
	if err != nil {
		return APIKey{}, "", err
	}
	id, err := randomToken(12)
	if err != nil {
		return APIKey{}, "", err
	}
	prefix := secret
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	now := time.Now().Unix()
	var creator any
	var creatorPtr *int64
	if creatorUserID > 0 {
		creator = creatorUserID
		value := creatorUserID
		creatorPtr = &value
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO api_keys(id,name,prefix,token_hash,created_by_user_id,created_at) VALUES(?,?,?,?,?,?)", id, name, prefix, tokenHash(secret), creator, now); err != nil {
		return APIKey{}, "", err
	}
	return APIKey{ID: id, Name: name, Prefix: prefix, Enabled: true, CreatedByUserID: creatorPtr, CreatedAt: now}, secret, nil
}

func (s *Service) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,name,prefix,enabled,created_by_user_id,created_at,last_used_at,revoked_at FROM api_keys ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]APIKey, 0)
	for rows.Next() {
		var item APIKey
		var enabled int
		var creator, lastUsed, revoked sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Name, &item.Prefix, &enabled, &creator, &item.CreatedAt, &lastUsed, &revoked); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		if creator.Valid {
			value := creator.Int64
			item.CreatedByUserID = &value
		}
		if lastUsed.Valid {
			value := lastUsed.Int64
			item.LastUsedAt = &value
		}
		if revoked.Valid {
			value := revoked.Int64
			item.RevokedAt = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) SetAPIKeyEnabled(ctx context.Context, id string, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	query := "UPDATE api_keys SET enabled=? WHERE id=?"
	if enabled {
		query += " AND revoked_at IS NULL"
	}
	result, err := s.db.ExecContext(ctx, query, value, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Service) RevokeAPIKey(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE api_keys SET enabled=0, revoked_at=COALESCE(revoked_at,?) WHERE id=?", time.Now().Unix(), id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Service) RotateAPIKey(ctx context.Context, id string, creatorUserID int64) (APIKey, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APIKey{}, "", err
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT name FROM api_keys WHERE id=? AND revoked_at IS NULL", id).Scan(&name); err != nil {
		return APIKey{}, "", err
	}
	secret, err := randomToken(32)
	if err != nil {
		return APIKey{}, "", err
	}
	newID, err := randomToken(12)
	if err != nil {
		return APIKey{}, "", err
	}
	prefix := secret
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	now := time.Now().Unix()
	var creator any
	var creatorPtr *int64
	if creatorUserID > 0 {
		creator = creatorUserID
		value := creatorUserID
		creatorPtr = &value
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO api_keys(id,name,prefix,token_hash,created_by_user_id,created_at) VALUES(?,?,?,?,?,?)", newID, name, prefix, tokenHash(secret), creator, now); err != nil {
		return APIKey{}, "", err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE api_keys SET enabled=0,revoked_at=? WHERE id=?", now, id); err != nil {
		return APIKey{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return APIKey{}, "", err
	}
	return APIKey{ID: newID, Name: name, Prefix: prefix, Enabled: true, CreatedByUserID: creatorPtr, CreatedAt: now}, secret, nil
}

func (s *Service) AuthenticateAPIKey(ctx context.Context, token string) error {
	if token == "" {
		return errors.New("missing api key")
	}
	var id string
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM api_keys WHERE token_hash=? AND enabled=1 AND revoked_at IS NULL", tokenHash(token)).Scan(&id); err != nil {
		return errors.New("invalid api key")
	}
	now := time.Now()
	if !s.shouldPersistAPIUse(id, now) {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE api_keys SET last_used_at=? WHERE id=? AND enabled=1 AND revoked_at IS NULL", now.Unix(), id); err != nil {
		return err
	}
	return nil
}

func (s *Service) shouldPersistAPIUse(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.lastAPIKeyWrite[id]; ok && now.Sub(last) < apiUseWriteEvery {
		return false
	}
	s.lastAPIKeyWrite[id] = now
	return true
}
