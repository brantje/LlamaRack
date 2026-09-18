package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/brantje/llamarack/backend/internal/database"
)

type apiKeyInsert struct {
	ID                    string
	Name                  string
	Prefix                string
	TokenHash             string
	KeyType               string
	OwnerUserID           *int64
	OwnerServiceAccountID string
	InstanceIDs           []string
	ExpiresOn             string
	CreatedByUserID       *int64
	CreatedAt             int64
}

type apiKeyUpdate struct {
	Name                  string
	OwnerUserID           *int64
	OwnerServiceAccountID string
	InstanceIDs           []string
	ExpiresOn             string
	Enabled               bool
}

// APIKeyStore owns API-key persistence, owner lookups, and usage timestamps.
// Cache and authorization policy stay in Service.
type APIKeyStore interface {
	Insert(context.Context, apiKeyInsert) error
	List(context.Context, string) ([]APIKey, error)
	GetByID(context.Context, string) (APIKey, error)
	GetByHash(context.Context, string) (APIKey, error)
	SetEnabled(context.Context, string, bool) error
	Update(context.Context, string, apiKeyUpdate) error
	RotateSecret(context.Context, string, string, string) error
	TouchLastUsed(context.Context, string, int64) error
	OwnerEnabled(context.Context, *int64, string) (bool, error)
	LiveInstanceIDs(context.Context) (map[string]struct{}, error)
	ServiceAccountHidden(context.Context, string) (bool, error)
}

type sqlAPIKeyStore struct{ db database.Store }

func NewAPIKeyStore(db database.Store) APIKeyStore { return &sqlAPIKeyStore{db: db} }

func (s *sqlAPIKeyStore) Insert(ctx context.Context, in apiKeyInsert) error {
	instanceJSON, err := json.Marshal(in.InstanceIDs)
	if err != nil {
		return err
	}
	var ownerUser, ownerSA, expires, creator any
	if in.OwnerUserID != nil {
		ownerUser = *in.OwnerUserID
	}
	if in.OwnerServiceAccountID != "" {
		ownerSA = in.OwnerServiceAccountID
	}
	if in.ExpiresOn != "" {
		expires = in.ExpiresOn
	}
	if in.CreatedByUserID != nil && *in.CreatedByUserID > 0 {
		creator = *in.CreatedByUserID
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO api_keys(
		id,name,prefix,token_hash,key_type,owner_user_id,owner_service_account_id,enabled,expires_on,instance_ids,created_by_user_id,created_at
	) VALUES(?,?,?,?,?,?,?,1,?,?,?,?)`,
		in.ID, in.Name, in.Prefix, in.TokenHash, in.KeyType, ownerUser, ownerSA, expires, string(instanceJSON), creator, in.CreatedAt)
	return database.ClassifyError(err)
}

const apiKeySelectSQL = `SELECT k.id,k.name,k.prefix,k.key_type,k.enabled,k.expires_on,k.instance_ids,k.created_by_user_id,k.created_at,k.last_used_at,
	k.owner_user_id,k.owner_service_account_id,u.username,u.enabled,sa.name,sa.enabled,COALESCE(sa.hidden,0)
	FROM api_keys k
	LEFT JOIN users u ON u.id=k.owner_user_id
	LEFT JOIN service_accounts sa ON sa.id=k.owner_service_account_id`

func (s *sqlAPIKeyStore) List(ctx context.Context, serviceAccountID string) ([]APIKey, error) {
	query := apiKeySelectSQL
	args := []any{}
	if serviceAccountID != "" {
		query += " WHERE k.owner_service_account_id=?"
		args = append(args, serviceAccountID)
	}
	query += " ORDER BY k.created_at DESC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	var items []APIKey
	for rows.Next() {
		item, err := scanStoredAPIKey(rows)
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

func (s *sqlAPIKeyStore) GetByID(ctx context.Context, id string) (APIKey, error) {
	return scanStoredAPIKey(s.db.QueryRowContext(ctx, apiKeySelectSQL+" WHERE k.id=?", id))
}

func (s *sqlAPIKeyStore) GetByHash(ctx context.Context, hash string) (APIKey, error) {
	return scanStoredAPIKey(s.db.QueryRowContext(ctx, apiKeySelectSQL+" WHERE k.token_hash=?", hash))
}

func (s *sqlAPIKeyStore) SetEnabled(ctx context.Context, id string, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	return updateAPIKeyOne(ctx, s.db, "UPDATE api_keys SET enabled=? WHERE id=?", value, id)
}

func (s *sqlAPIKeyStore) Update(ctx context.Context, id string, in apiKeyUpdate) error {
	instanceJSON, err := json.Marshal(in.InstanceIDs)
	if err != nil {
		return err
	}
	var ownerUser, ownerSA, expires any
	if in.OwnerUserID != nil {
		ownerUser = *in.OwnerUserID
	}
	if in.OwnerServiceAccountID != "" {
		ownerSA = in.OwnerServiceAccountID
	}
	if in.ExpiresOn != "" {
		expires = in.ExpiresOn
	}
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	return updateAPIKeyOne(ctx, s.db, `UPDATE api_keys SET name=?,owner_user_id=?,owner_service_account_id=?,instance_ids=?,expires_on=?,enabled=? WHERE id=?`,
		in.Name, ownerUser, ownerSA, string(instanceJSON), expires, enabled, id)
}

func (s *sqlAPIKeyStore) RotateSecret(ctx context.Context, id, prefix, hash string) error {
	return updateAPIKeyOne(ctx, s.db, "UPDATE api_keys SET prefix=?,token_hash=? WHERE id=?", prefix, hash, id)
}

func (s *sqlAPIKeyStore) TouchLastUsed(ctx context.Context, id string, at int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE api_keys SET last_used_at=? WHERE id=? AND enabled=1", at, id)
	return database.ClassifyError(err)
}

func updateAPIKeyOne(ctx context.Context, db database.Store, query string, args ...any) error {
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

func (s *sqlAPIKeyStore) OwnerEnabled(ctx context.Context, userID *int64, serviceAccountID string) (bool, error) {
	var enabled int
	var err error
	if userID != nil && *userID > 0 {
		err = s.db.QueryRowContext(ctx, "SELECT enabled FROM users WHERE id=?", *userID).Scan(&enabled)
	} else {
		err = s.db.QueryRowContext(ctx, "SELECT enabled FROM service_accounts WHERE id=?", serviceAccountID).Scan(&enabled)
	}
	if err != nil {
		return false, database.ClassifyError(err)
	}
	return enabled != 0, nil
}

func (s *sqlAPIKeyStore) LiveInstanceIDs(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM instances")
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, database.ClassifyError(err)
		}
		out[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlAPIKeyStore) ServiceAccountHidden(ctx context.Context, id string) (bool, error) {
	var hidden int
	if err := s.db.QueryRowContext(ctx, "SELECT hidden FROM service_accounts WHERE id=?", strings.TrimSpace(id)).Scan(&hidden); err != nil {
		return false, database.ClassifyError(err)
	}
	return hidden != 0, nil
}

func scanStoredAPIKey(row interface{ Scan(...any) error }) (APIKey, error) {
	var item APIKey
	var enabled int
	var expiresOn sql.NullString
	var instanceJSON string
	var creator, lastUsed, ownerUserID sql.NullInt64
	var ownerServiceAccountID, userName, saName sql.NullString
	var userEnabled, saEnabled, saHidden sql.NullInt64
	if err := row.Scan(
		&item.ID, &item.Name, &item.Prefix, &item.KeyType, &enabled, &expiresOn, &instanceJSON, &creator, &item.CreatedAt, &lastUsed,
		&ownerUserID, &ownerServiceAccountID, &userName, &userEnabled, &saName, &saEnabled, &saHidden,
	); err != nil {
		return APIKey{}, database.ClassifyError(err)
	}
	item.Enabled = enabled != 0
	item.InstanceIDs = decodeInstanceIDs(instanceJSON)
	if expiresOn.Valid && strings.TrimSpace(expiresOn.String) != "" {
		value := expiresOn.String
		item.ExpiresOn = &value
	}
	if creator.Valid {
		value := creator.Int64
		item.CreatedByUserID = &value
	}
	if lastUsed.Valid {
		value := lastUsed.Int64
		item.LastUsedAt = &value
	}
	switch {
	case ownerUserID.Valid:
		item.OwnerKind = OwnerKindUser
		item.OwnerID = strconv.FormatInt(ownerUserID.Int64, 10)
		item.OwnerName = userName.String
		item.OwnerEnabled = userEnabled.Valid && userEnabled.Int64 != 0
	case ownerServiceAccountID.Valid:
		item.OwnerKind = OwnerKindServiceAccount
		item.OwnerID = ownerServiceAccountID.String
		item.OwnerName = saName.String
		item.OwnerEnabled = saEnabled.Valid && saEnabled.Int64 != 0
		item.HiddenOwner = saHidden.Valid && saHidden.Int64 != 0
		item.Managed = item.HiddenOwner && item.Name == ManagedPrincipalName && item.OwnerName == ManagedPrincipalName
	}
	if item.InstanceIDs == nil {
		item.InstanceIDs = []string{}
	}
	return item, nil
}

var _ APIKeyStore = (*sqlAPIKeyStore)(nil)
