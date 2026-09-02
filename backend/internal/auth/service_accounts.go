package auth

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func (s *Service) CreateServiceAccount(ctx context.Context, name string, createdByUserID int64) (ServiceAccount, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ServiceAccount{}, ErrServiceAccountNameRequired
	}
	id, err := randomToken(12)
	if err != nil {
		return ServiceAccount{}, err
	}
	now := time.Now().Unix()
	var creator any
	var creatorPtr *int64
	if createdByUserID > 0 {
		creator = createdByUserID
		value := createdByUserID
		creatorPtr = &value
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO service_accounts(id,name,enabled,created_at,created_by_user_id) VALUES(?,?,1,?,?)", id, name, now, creator); err != nil {
		return ServiceAccount{}, err
	}
	return ServiceAccount{ID: id, Name: name, Enabled: true, CreatedAt: now, CreatedByUserID: creatorPtr}, nil
}

func (s *Service) ListServiceAccounts(ctx context.Context) ([]ServiceAccount, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,name,enabled,created_at,created_by_user_id FROM service_accounts ORDER BY name COLLATE NOCASE, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ServiceAccount, 0)
	for rows.Next() {
		item, err := scanServiceAccount(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) GetServiceAccount(ctx context.Context, id string) (ServiceAccount, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ServiceAccount{}, sql.ErrNoRows
	}
	item, err := scanServiceAccount(s.db.QueryRowContext(ctx, "SELECT id,name,enabled,created_at,created_by_user_id FROM service_accounts WHERE id=?", id))
	if err != nil {
		return ServiceAccount{}, err
	}
	keys, err := s.ListAPIKeysForServiceAccount(ctx, id)
	if err != nil {
		return ServiceAccount{}, err
	}
	item.Keys = keys
	return item, nil
}

func (s *Service) UpdateServiceAccount(ctx context.Context, id string, name *string, enabled *bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return sql.ErrNoRows
	}
	existing, err := scanServiceAccount(s.db.QueryRowContext(ctx, "SELECT id,name,enabled,created_at,created_by_user_id FROM service_accounts WHERE id=?", id))
	if err != nil {
		return err
	}
	nextName := existing.Name
	if name != nil {
		nextName = strings.TrimSpace(*name)
		if nextName == "" {
			return ErrServiceAccountNameRequired
		}
	}
	nextEnabled := 0
	if existing.Enabled {
		nextEnabled = 1
	}
	if enabled != nil {
		nextEnabled = 0
		if *enabled {
			nextEnabled = 1
		}
	}
	result, err := s.db.ExecContext(ctx, "UPDATE service_accounts SET name=?, enabled=? WHERE id=?", nextName, nextEnabled, id)
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

func (s *Service) DeleteServiceAccount(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return sql.ErrNoRows
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM service_accounts WHERE id=?", id)
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

type serviceAccountRow interface {
	Scan(dest ...any) error
}

func scanServiceAccount(row serviceAccountRow) (ServiceAccount, error) {
	var item ServiceAccount
	var enabled int
	var creator sql.NullInt64
	if err := row.Scan(&item.ID, &item.Name, &enabled, &item.CreatedAt, &creator); err != nil {
		return ServiceAccount{}, err
	}
	item.Enabled = enabled != 0
	if creator.Valid {
		value := creator.Int64
		item.CreatedByUserID = &value
	}
	return item, nil
}
