package auth

import (
	"context"
	"database/sql"

	"github.com/brantje/llamarack/backend/internal/database"
)

// ServiceAccountStore owns durable service-account persistence.
type ServiceAccountStore interface {
	Create(context.Context, ServiceAccount) error
	ListVisible(context.Context) ([]ServiceAccount, error)
	Get(context.Context, string) (ServiceAccount, error)
	FindByName(context.Context, string, bool) (ServiceAccount, error)
	Update(context.Context, string, string, bool) error
	Delete(context.Context, string) error
}

type sqlServiceAccountStore struct{ db database.Store }

func NewServiceAccountStore(db database.Store) ServiceAccountStore {
	return &sqlServiceAccountStore{db: db}
}

func (s *sqlServiceAccountStore) Create(ctx context.Context, item ServiceAccount) error {
	var creator any
	if item.CreatedByUserID != nil {
		creator = *item.CreatedByUserID
	}
	hidden := 0
	if item.Hidden {
		hidden = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO service_accounts(id,name,enabled,hidden,created_at,created_by_user_id) VALUES(?,?,1,?,?,?)`,
		item.ID, item.Name, hidden, item.CreatedAt, creator)
	return database.ClassifyError(err)
}

func (s *sqlServiceAccountStore) ListVisible(ctx context.Context) ([]ServiceAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,enabled,hidden,created_at,created_by_user_id FROM service_accounts WHERE hidden=0 ORDER BY LOWER(name),name,id`)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	items := make([]ServiceAccount, 0)
	for rows.Next() {
		item, err := scanStoredServiceAccount(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return items, nil
}

func (s *sqlServiceAccountStore) Get(ctx context.Context, id string) (ServiceAccount, error) {
	return scanStoredServiceAccount(s.db.QueryRowContext(ctx, `SELECT id,name,enabled,hidden,created_at,created_by_user_id FROM service_accounts WHERE id=?`, id))
}

func (s *sqlServiceAccountStore) FindByName(ctx context.Context, name string, hiddenOnly bool) (ServiceAccount, error) {
	query := `SELECT id,name,enabled,hidden,created_at,created_by_user_id FROM service_accounts WHERE name=?`
	if hiddenOnly {
		query += " AND hidden=1"
	}
	return scanStoredServiceAccount(s.db.QueryRowContext(ctx, query, name))
}

func (s *sqlServiceAccountStore) Update(ctx context.Context, id, name string, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	return updateServiceAccountOne(ctx, s.db, `UPDATE service_accounts SET name=?,enabled=? WHERE id=?`, name, value, id)
}

func (s *sqlServiceAccountStore) Delete(ctx context.Context, id string) error {
	return updateServiceAccountOne(ctx, s.db, `DELETE FROM service_accounts WHERE id=?`, id)
}

func updateServiceAccountOne(ctx context.Context, db database.Store, query string, args ...any) error {
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return database.ClassifyError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return database.ClassifyError(err)
	}
	if n != 1 {
		return database.ClassifyError(sql.ErrNoRows)
	}
	return nil
}

func scanStoredServiceAccount(row interface{ Scan(...any) error }) (ServiceAccount, error) {
	var item ServiceAccount
	var enabled, hidden int
	var creator sql.NullInt64
	if err := row.Scan(&item.ID, &item.Name, &enabled, &hidden, &item.CreatedAt, &creator); err != nil {
		return ServiceAccount{}, database.ClassifyError(err)
	}
	item.Enabled = enabled != 0
	item.Hidden = hidden != 0
	if creator.Valid {
		value := creator.Int64
		item.CreatedByUserID = &value
	}
	return item, nil
}

var _ ServiceAccountStore = (*sqlServiceAccountStore)(nil)
