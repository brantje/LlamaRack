package auth

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/brantje/llamarack/backend/internal/database"
)

// OIDCStore owns provider configuration and external-identity persistence.
type OIDCStore interface {
	CreateProvider(context.Context, OIDCProvider) error
	UpdateProvider(context.Context, OIDCProvider) error
	DeleteProvider(context.Context, string) error
	GetProvider(context.Context, string) (OIDCProvider, error)
	ListProviders(context.Context) ([]OIDCProvider, error)
	PublicProviders(context.Context) ([]PublicOIDCProvider, error)
	SetProviderTestResult(context.Context, string, int64, bool) error
	HasUsableProvider(context.Context, string) (bool, error)
	IdentityUserID(context.Context, string, string, string) (int64, error)
	UserIDByUsername(context.Context, string) (int64, error)
	CreateJITUser(context.Context, string, string, string, string, int64) (User, error)
	ListIdentities(context.Context, int64) ([]ExternalIdentity, error)
	LinkIdentity(context.Context, ExternalIdentity) error
	UnlinkIdentity(context.Context, string) error
	UnlinkOwnIdentity(context.Context, int64, string) error
}

type sqlOIDCStore struct{ db database.Store }

func NewOIDCStore(db database.Store) OIDCStore { return &sqlOIDCStore{db: db} }

func (s *sqlOIDCStore) CreateProvider(ctx context.Context, p OIDCProvider) error {
	scopes, err := json.Marshal(p.Scopes)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO oidc_providers(
		id,name,enabled,issuer,discovery_url,client_id,scopes,username_claim,authorization_endpoint,token_endpoint,jwks_url,last_tested_at,last_test_succeeded,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, boolInt(p.Enabled), p.Issuer, p.DiscoveryURL, p.ClientID, string(scopes), p.UsernameClaim,
		p.AuthorizationEndpoint, p.TokenEndpoint, p.JWKSURL, nullableInt64(p.LastTestedAt), boolInt(p.LastTestSucceeded), p.CreatedAt, p.UpdatedAt)
	return database.ClassifyError(err)
}

func (s *sqlOIDCStore) UpdateProvider(ctx context.Context, p OIDCProvider) error {
	scopes, err := json.Marshal(p.Scopes)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE oidc_providers SET
		name=?,enabled=?,issuer=?,discovery_url=?,client_id=?,scopes=?,username_claim=?,authorization_endpoint=?,token_endpoint=?,jwks_url=?,
		last_tested_at=?,last_test_succeeded=?,updated_at=? WHERE id=?`,
		p.Name, boolInt(p.Enabled), p.Issuer, p.DiscoveryURL, p.ClientID, string(scopes), p.UsernameClaim,
		p.AuthorizationEndpoint, p.TokenEndpoint, p.JWKSURL, nullableInt64(p.LastTestedAt), boolInt(p.LastTestSucceeded), p.UpdatedAt, p.ID)
	if err != nil {
		return database.ClassifyError(err)
	}
	return requireOneRow(result)
}

func (s *sqlOIDCStore) DeleteProvider(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM oidc_providers WHERE id=?", id)
	if err != nil {
		return database.ClassifyError(err)
	}
	return requireOneRow(result)
}

const oidcProviderColumns = `id,name,enabled,issuer,discovery_url,client_id,scopes,username_claim,authorization_endpoint,token_endpoint,jwks_url,last_tested_at,last_test_succeeded,created_at,updated_at`

func (s *sqlOIDCStore) GetProvider(ctx context.Context, id string) (OIDCProvider, error) {
	return scanStoredOIDCProvider(s.db.QueryRowContext(ctx, `SELECT `+oidcProviderColumns+` FROM oidc_providers WHERE id=?`, id))
}

func (s *sqlOIDCStore) ListProviders(ctx context.Context) ([]OIDCProvider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+oidcProviderColumns+` FROM oidc_providers ORDER BY LOWER(name),name`)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	var out []OIDCProvider
	for rows.Next() {
		p, err := scanStoredOIDCProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlOIDCStore) PublicProviders(ctx context.Context) ([]PublicOIDCProvider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name FROM oidc_providers WHERE enabled=1 ORDER BY LOWER(name),name`)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	var out []PublicOIDCProvider
	for rows.Next() {
		var item PublicOIDCProvider
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, database.ClassifyError(err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlOIDCStore) SetProviderTestResult(ctx context.Context, id string, at int64, succeeded bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE oidc_providers SET last_tested_at=?,last_test_succeeded=?,updated_at=? WHERE id=?`, at, boolInt(succeeded), at, id)
	if err != nil {
		return database.ClassifyError(err)
	}
	return requireOneRow(result)
}

func (s *sqlOIDCStore) HasUsableProvider(ctx context.Context, excludeID string) (bool, error) {
	query := "SELECT COUNT(*) FROM oidc_providers WHERE enabled=1 AND last_test_succeeded=1"
	args := []any{}
	if excludeID != "" {
		query += " AND id<>?"
		args = append(args, excludeID)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, database.ClassifyError(err)
	}
	return count > 0, nil
}

func (s *sqlOIDCStore) IdentityUserID(ctx context.Context, providerID, issuer, subject string) (int64, error) {
	var userID int64
	if err := s.db.QueryRowContext(ctx, `SELECT user_id FROM external_identities WHERE provider_id=? AND issuer=? AND subject=?`, providerID, issuer, subject).Scan(&userID); err != nil {
		return 0, database.ClassifyError(err)
	}
	return userID, nil
}

func (s *sqlOIDCStore) UserIDByUsername(ctx context.Context, username string) (int64, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE LOWER(username)=LOWER(?)`, username).Scan(&id); err != nil {
		return 0, database.ClassifyError(err)
	}
	return id, nil
}

func (s *sqlOIDCStore) CreateJITUser(ctx context.Context, username, providerID, issuer, subject string, now int64) (User, error) {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO users(username,password_hash,enabled,created_at) VALUES(?,?,1,?) RETURNING id`, username, "!oidc", now).Scan(&id); err != nil {
		return User{}, database.ClassifyError(err)
	}
	identityID, err := randomToken(12)
	if err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO external_identities(id,provider_id,issuer,subject,user_id,created_at) VALUES(?,?,?,?,?,?)`,
		identityID, providerID, issuer, subject, id, now); err != nil {
		return User{}, database.ClassifyError(err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, database.ClassifyError(err)
	}
	return User{ID: id, Username: username, Enabled: true, CreatedAt: now}, nil
}

func (s *sqlOIDCStore) ListIdentities(ctx context.Context, userID int64) ([]ExternalIdentity, error) {
	query := "SELECT id,provider_id,issuer,subject,user_id,created_at FROM external_identities"
	args := []any{}
	if userID > 0 {
		query += " WHERE user_id=?"
		args = append(args, userID)
	}
	query += " ORDER BY created_at,id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	var out []ExternalIdentity
	for rows.Next() {
		var item ExternalIdentity
		if err := rows.Scan(&item.ID, &item.ProviderID, &item.Issuer, &item.Subject, &item.UserID, &item.CreatedAt); err != nil {
			return nil, database.ClassifyError(err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlOIDCStore) LinkIdentity(ctx context.Context, item ExternalIdentity) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO external_identities(id,provider_id,issuer,subject,user_id,created_at) VALUES(?,?,?,?,?,?)`,
		item.ID, item.ProviderID, item.Issuer, item.Subject, item.UserID, item.CreatedAt)
	return database.ClassifyError(err)
}

func (s *sqlOIDCStore) UnlinkIdentity(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM external_identities WHERE id=?", id)
	if err != nil {
		return database.ClassifyError(err)
	}
	return requireOneRow(result)
}

func (s *sqlOIDCStore) UnlinkOwnIdentity(ctx context.Context, userID int64, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM external_identities WHERE id=? AND user_id=?", id, userID)
	if err != nil {
		return database.ClassifyError(err)
	}
	return requireOneRow(result)
}

func scanStoredOIDCProvider(row interface{ Scan(...any) error }) (OIDCProvider, error) {
	var p OIDCProvider
	var enabled, tested int
	var scopesRaw string
	var testedAt sql.NullInt64
	if err := row.Scan(&p.ID, &p.Name, &enabled, &p.Issuer, &p.DiscoveryURL, &p.ClientID, &scopesRaw, &p.UsernameClaim,
		&p.AuthorizationEndpoint, &p.TokenEndpoint, &p.JWKSURL, &testedAt, &tested, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return OIDCProvider{}, database.ClassifyError(err)
	}
	p.Enabled, p.LastTestSucceeded = enabled != 0, tested != 0
	if testedAt.Valid {
		v := testedAt.Int64
		p.LastTestedAt = &v
	}
	if err := json.Unmarshal([]byte(scopesRaw), &p.Scopes); err != nil || len(p.Scopes) == 0 {
		p.Scopes = []string{"openid"}
	}
	return p, nil
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func requireOneRow(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return database.ClassifyError(err)
	}
	if n != 1 {
		return database.ClassifyError(sql.ErrNoRows)
	}
	return nil
}

var _ OIDCStore = (*sqlOIDCStore)(nil)
