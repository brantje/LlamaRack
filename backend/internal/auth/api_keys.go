package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (s *Service) CreateAPIKeyForUser(ctx context.Context, name string, ownerUserID int64) (APIKey, string, error) {
	var owner *int64
	var creator *int64
	if ownerUserID > 0 {
		value := ownerUserID
		owner = &value
		creator = &value
	}
	return s.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name:            name,
		KeyType:         APIKeyTypeInference,
		OwnerUserID:     owner,
		CreatedByUserID: creator,
	})
}

func (s *Service) CreateAPIKey(ctx context.Context, in CreateAPIKeyInput) (APIKey, string, error) {
	name, keyType, ownerUserID, ownerServiceAccountID, instanceIDs, expiresOn, err := s.normalizeAPIKeyWrite(ctx, in.Name, in.KeyType, in.OwnerUserID, in.OwnerServiceAccountID, in.InstanceIDs, in.ExpiresOn, true, true, true)
	if err != nil {
		return APIKey{}, "", err
	}
	secret, prefix, err := generateAPIKeySecret()
	if err != nil {
		return APIKey{}, "", err
	}
	id, err := randomToken(12)
	if err != nil {
		return APIKey{}, "", err
	}
	now := time.Now().Unix()
	var creator any
	if in.CreatedByUserID != nil && *in.CreatedByUserID > 0 {
		creator = *in.CreatedByUserID
	}
	instanceJSON, err := json.Marshal(instanceIDs)
	if err != nil {
		return APIKey{}, "", err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO api_keys(
		id,name,prefix,token_hash,key_type,owner_user_id,owner_service_account_id,enabled,expires_on,instance_ids,created_by_user_id,created_at
	) VALUES(?,?,?,?,?,?,?,1,?,?,?,?)`,
		id, name, prefix, tokenHash(secret), keyType, ownerUserID, ownerServiceAccountID, expiresOn, string(instanceJSON), creator, now,
	); err != nil {
		return APIKey{}, "", err
	}
	item, err := s.getAPIKey(ctx, id)
	if err != nil {
		return APIKey{}, "", err
	}
	return item, secret, nil
}

func (s *Service) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	return s.listAPIKeys(ctx, "")
}

func (s *Service) ListAPIKeysForServiceAccount(ctx context.Context, serviceAccountID string) ([]APIKey, error) {
	serviceAccountID = strings.TrimSpace(serviceAccountID)
	if serviceAccountID == "" {
		return nil, sql.ErrNoRows
	}
	return s.listAPIKeys(ctx, serviceAccountID)
}

func (s *Service) listAPIKeys(ctx context.Context, serviceAccountID string) ([]APIKey, error) {
	liveIDs, err := s.liveInstanceIDs(ctx)
	if err != nil {
		return nil, err
	}
	query := apiKeySelectSQL + " ORDER BY k.created_at DESC"
	args := []any{}
	if serviceAccountID != "" {
		query = apiKeySelectSQL + " WHERE k.owner_service_account_id=? ORDER BY k.created_at DESC"
		args = append(args, serviceAccountID)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]APIKey, 0)
	for rows.Next() {
		item, err := scanAPIKey(rows, liveIDs)
		if err != nil {
			return nil, err
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
	result, err := s.db.ExecContext(ctx, "UPDATE api_keys SET enabled=? WHERE id=?", value, id)
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

func (s *Service) UpdateAPIKey(ctx context.Context, id string, in UpdateAPIKeyInput) error {
	existing, err := s.getAPIKey(ctx, id)
	if err != nil {
		return err
	}
	name := existing.Name
	if in.Name != nil {
		name = *in.Name
	}
	var ownerUserID *int64
	ownerServiceAccountID := ""
	switch existing.OwnerKind {
	case OwnerKindUser:
		parsed, parseErr := strconv.ParseInt(existing.OwnerID, 10, 64)
		if parseErr != nil {
			return parseErr
		}
		ownerUserID = &parsed
	case OwnerKindServiceAccount:
		ownerServiceAccountID = existing.OwnerID
	}
	if in.OwnerUserID != nil || in.OwnerServiceAccountID != nil {
		ownerUserID = in.OwnerUserID
		ownerServiceAccountID = ""
		if in.OwnerServiceAccountID != nil {
			ownerServiceAccountID = *in.OwnerServiceAccountID
		}
	}
	instanceIDs := existing.InstanceIDs
	if in.InstanceIDs != nil {
		instanceIDs = *in.InstanceIDs
	}
	expiresOn := ""
	if existing.ExpiresOn != nil {
		expiresOn = *existing.ExpiresOn
	}
	if in.ClearExpiresOn {
		expiresOn = ""
	} else if in.ExpiresOn != nil {
		expiresOn = *in.ExpiresOn
	}
	ownerChanged := in.OwnerUserID != nil || in.OwnerServiceAccountID != nil
	name, _, ownerUserValue, ownerSAValue, instanceIDs, expiresOnValue, err := s.normalizeAPIKeyWrite(ctx, name, existing.KeyType, ownerUserID, ownerServiceAccountID, instanceIDs, expiresOn, in.ExpiresOn != nil, ownerChanged, in.InstanceIDs != nil)
	if err != nil {
		return err
	}
	enabled := 0
	if existing.Enabled {
		enabled = 1
	}
	if in.Enabled != nil {
		enabled = 0
		if *in.Enabled {
			enabled = 1
		}
	}
	instanceJSON, err := json.Marshal(instanceIDs)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE api_keys SET name=?, owner_user_id=?, owner_service_account_id=?, instance_ids=?, expires_on=?, enabled=? WHERE id=?`,
		name, ownerUserValue, ownerSAValue, string(instanceJSON), expiresOnValue, enabled, id)
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

func (s *Service) RotateAPIKey(ctx context.Context, id string) (APIKey, string, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM api_keys WHERE id=?", id).Scan(&exists); err != nil {
		return APIKey{}, "", err
	}
	secret, prefix, err := generateAPIKeySecret()
	if err != nil {
		return APIKey{}, "", err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE api_keys SET prefix=?, token_hash=? WHERE id=?", prefix, tokenHash(secret), id)
	if err != nil {
		return APIKey{}, "", err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return APIKey{}, "", err
	}
	if rows != 1 {
		return APIKey{}, "", sql.ErrNoRows
	}
	s.clearAPIUseWrite(id)
	item, err := s.getAPIKey(ctx, id)
	if err != nil {
		return APIKey{}, "", err
	}
	return item, secret, nil
}

func (s *Service) AuthenticateAPIKey(ctx context.Context, token string) error {
	_, err := s.AuthenticateAPIKeyInfo(ctx, token)
	return err
}

// AuthenticateAPIKeyInfo validates a key and returns only its safe identity.
// The raw secret is never retained or returned.
func (s *Service) AuthenticateAPIKeyInfo(ctx context.Context, token string) (APIKey, error) {
	if trustedInferenceContext(ctx) {
		return APIKey{Name: "Management Playground", Enabled: true, KeyType: APIKeyTypeFull, Status: APIKeyStatusEnabled, InstanceIDs: []string{}}, nil
	}
	if token == "" {
		return APIKey{}, ErrAPIKeyMissing
	}
	if !strings.HasPrefix(token, apiKeySecretPrefix) {
		return APIKey{}, ErrAPIKeyInvalid
	}
	item, err := s.lookupAPIKeyByHash(ctx, tokenHash(token))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return APIKey{}, ErrAPIKeyInvalid
		}
		return APIKey{}, err
	}
	if !item.Enabled || !item.OwnerEnabled || apiKeyExpired(item.ExpiresOn, time.Now().UTC()) {
		return APIKey{}, ErrAPIKeyInvalid
	}

	now := time.Now()
	if item.LastUsedAt != nil && now.Unix()-*item.LastUsedAt < int64(apiUseWriteEvery/time.Second) {
		return item, nil
	}
	if !s.reserveAPIUseWrite(item.ID, now) {
		return item, nil
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE api_keys SET last_used_at=? WHERE id=? AND enabled=1", now.Unix(), item.ID); err != nil {
		s.releaseAPIUseWrite(item.ID, now)
		return APIKey{}, err
	}
	value := now.Unix()
	item.LastUsedAt = &value
	return item, nil
}

func (s *Service) reserveAPIUseWrite(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.lastAPIKeyWrite[id]; ok && now.Sub(last) < apiUseWriteEvery {
		return false
	}
	s.lastAPIKeyWrite[id] = now
	return true
}

func (s *Service) releaseAPIUseWrite(id string, reserved time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.lastAPIKeyWrite[id]; ok && current.Equal(reserved) {
		delete(s.lastAPIKeyWrite, id)
	}
}

func (s *Service) clearAPIUseWrite(id string) {
	s.mu.Lock()
	delete(s.lastAPIKeyWrite, id)
	s.mu.Unlock()
}

const apiKeySelectSQL = `SELECT k.id,k.name,k.prefix,k.key_type,k.enabled,k.expires_on,k.instance_ids,k.created_by_user_id,k.created_at,k.last_used_at,
	k.owner_user_id,k.owner_service_account_id,u.username,u.enabled,sa.name,sa.enabled
	FROM api_keys k
	LEFT JOIN users u ON u.id=k.owner_user_id
	LEFT JOIN service_accounts sa ON sa.id=k.owner_service_account_id`

func (s *Service) getAPIKey(ctx context.Context, id string) (APIKey, error) {
	liveIDs, err := s.liveInstanceIDs(ctx)
	if err != nil {
		return APIKey{}, err
	}
	return scanAPIKey(s.db.QueryRowContext(ctx, apiKeySelectSQL+" WHERE k.id=?", id), liveIDs)
}

func (s *Service) lookupAPIKeyByHash(ctx context.Context, hash string) (APIKey, error) {
	liveIDs, err := s.liveInstanceIDs(ctx)
	if err != nil {
		return APIKey{}, err
	}
	return scanAPIKey(s.db.QueryRowContext(ctx, apiKeySelectSQL+" WHERE k.token_hash=?", hash), liveIDs)
}

type apiKeyRow interface {
	Scan(dest ...any) error
}

func scanAPIKey(row apiKeyRow, liveIDs map[string]struct{}) (APIKey, error) {
	var item APIKey
	var enabled int
	var expiresOn sql.NullString
	var instanceJSON string
	var creator, lastUsed, ownerUserID sql.NullInt64
	var ownerServiceAccountID sql.NullString
	var userName sql.NullString
	var userEnabled sql.NullInt64
	var saName sql.NullString
	var saEnabled sql.NullInt64
	if err := row.Scan(
		&item.ID, &item.Name, &item.Prefix, &item.KeyType, &enabled, &expiresOn, &instanceJSON, &creator, &item.CreatedAt, &lastUsed,
		&ownerUserID, &ownerServiceAccountID, &userName, &userEnabled, &saName, &saEnabled,
	); err != nil {
		return APIKey{}, err
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
	}
	item.MissingInstanceIDs = missingInstanceIDs(item.InstanceIDs, liveIDs)
	item.Status = computeAPIKeyStatus(item.Enabled, item.OwnerEnabled, item.ExpiresOn, time.Now().UTC())
	if item.InstanceIDs == nil {
		item.InstanceIDs = []string{}
	}
	return item, nil
}

func (s *Service) normalizeAPIKeyWrite(ctx context.Context, name, keyType string, ownerUserID *int64, ownerServiceAccountID string, instanceIDs []string, expiresOn string, requireExpiresFuture, validateOwnerEnabled, validateInstanceIDs bool) (string, string, any, any, []string, any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", nil, nil, nil, nil, ErrAPIKeyNameRequired
	}
	keyType = strings.TrimSpace(strings.ToLower(keyType))
	if keyType == "" {
		keyType = APIKeyTypeInference
	}
	switch keyType {
	case APIKeyTypeInference, APIKeyTypeManagement, APIKeyTypeFull:
	default:
		return "", "", nil, nil, nil, nil, ErrAPIKeyTypeInvalid
	}
	ownerServiceAccountID = strings.TrimSpace(ownerServiceAccountID)
	hasUser := ownerUserID != nil && *ownerUserID > 0
	hasSA := ownerServiceAccountID != ""
	if hasUser == hasSA {
		return "", "", nil, nil, nil, nil, ErrAPIKeyOwnerRequired
	}
	if hasUser {
		var enabled int
		if err := s.db.QueryRowContext(ctx, "SELECT enabled FROM users WHERE id=?", *ownerUserID).Scan(&enabled); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", "", nil, nil, nil, nil, ErrAPIKeyOwnerNotFound
			}
			return "", "", nil, nil, nil, nil, err
		}
		if validateOwnerEnabled && enabled == 0 {
			return "", "", nil, nil, nil, nil, ErrAPIKeyOwnerDisabled
		}
	} else {
		var enabled int
		if err := s.db.QueryRowContext(ctx, "SELECT enabled FROM service_accounts WHERE id=?", ownerServiceAccountID).Scan(&enabled); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", "", nil, nil, nil, nil, ErrAPIKeyOwnerNotFound
			}
			return "", "", nil, nil, nil, nil, err
		}
		if validateOwnerEnabled && enabled == 0 {
			return "", "", nil, nil, nil, nil, ErrAPIKeyOwnerDisabled
		}
	}
	if instanceIDs == nil {
		instanceIDs = []string{}
	}
	normalizedIDs := uniqueTrimmed(instanceIDs)
	if len(normalizedIDs) > 0 && keyType != APIKeyTypeInference {
		return "", "", nil, nil, nil, nil, ErrAPIKeyInstancesNotAllowed
	}
	if validateInstanceIDs {
		if err := s.rejectUnknownInstanceIDs(ctx, normalizedIDs); err != nil {
			return "", "", nil, nil, nil, nil, err
		}
	}
	var expiresValue any
	expiresOn = strings.TrimSpace(expiresOn)
	if expiresOn != "" {
		day, err := parseExpiresOn(expiresOn)
		if err != nil {
			return "", "", nil, nil, nil, nil, err
		}
		if requireExpiresFuture && day.Before(utcToday()) {
			return "", "", nil, nil, nil, nil, ErrAPIKeyExpiresOnPast
		}
		expiresValue = day.Format(time.DateOnly)
	}
	var saID any
	if hasUser {
		ownerUserID = cloneInt64(*ownerUserID)
	} else {
		ownerUserID = nil
		saID = ownerServiceAccountID
	}
	return name, keyType, ownerUserID, saID, normalizedIDs, expiresValue, nil
}

func cloneInt64(value int64) *int64 {
	return &value
}

func (s *Service) rejectUnknownInstanceIDs(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	live, err := s.liveInstanceIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, ok := live[id]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownInstanceID, id)
		}
	}
	return nil
}

func (s *Service) liveInstanceIDs(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM instances")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

func generateAPIKeySecret() (secret, prefix string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	randomPart := base64.RawURLEncoding.EncodeToString(raw)
	secret = apiKeySecretPrefix + randomPart
	prefixPart := randomPart
	if len(prefixPart) > 8 {
		prefixPart = prefixPart[:8]
	}
	return secret, apiKeySecretPrefix + prefixPart, nil
}

func decodeInstanceIDs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return []string{}
	}
	return uniqueTrimmed(ids)
}

func uniqueTrimmed(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func missingInstanceIDs(stored []string, live map[string]struct{}) []string {
	missing := make([]string, 0)
	for _, id := range stored {
		if _, ok := live[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

func computeAPIKeyStatus(enabled, ownerEnabled bool, expiresOn *string, now time.Time) string {
	if !enabled {
		return APIKeyStatusDisabled
	}
	if !ownerEnabled {
		return APIKeyStatusOwnerDisabled
	}
	if apiKeyExpired(expiresOn, now) {
		return APIKeyStatusExpired
	}
	return APIKeyStatusEnabled
}

func apiKeyExpired(expiresOn *string, now time.Time) bool {
	if expiresOn == nil || strings.TrimSpace(*expiresOn) == "" {
		return false
	}
	day, err := parseExpiresOn(*expiresOn)
	if err != nil {
		return true
	}
	return day.Before(utcTodayAt(now))
}

func parseExpiresOn(value string) (time.Time, error) {
	day, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(value), time.UTC)
	if err != nil {
		return time.Time{}, ErrAPIKeyExpiresOnInvalid
	}
	return day, nil
}

func utcToday() time.Time {
	return utcTodayAt(time.Now().UTC())
}

func utcTodayAt(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}
