package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
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
	name, keyType, ownerUserValue, ownerSAValue, instanceIDs, expiresOnValue, err := s.normalizeAPIKeyWrite(ctx, in.Name, in.KeyType, in.OwnerUserID, in.OwnerServiceAccountID, in.InstanceIDs, in.ExpiresOn, true, true, true)
	if err != nil {
		return APIKey{}, "", err
	}
	ownerUserID, ownerServiceAccountID := ownersFromNormalized(ownerUserValue, ownerSAValue)
	if err := s.rejectHiddenOrdinaryAPIKeyOwner(ctx, ownerServiceAccountID); err != nil {
		return APIKey{}, "", err
	}
	expiresOn := expiresOnString(expiresOnValue)
	return s.insertAPIKey(ctx, name, keyType, ownerUserID, ownerServiceAccountID, instanceIDs, expiresOn, in.CreatedByUserID)
}

func (s *Service) createAPIKey(ctx context.Context, in CreateAPIKeyInput) (APIKey, string, error) {
	name, keyType, ownerUserValue, ownerSAValue, instanceIDs, expiresOnValue, err := s.normalizeAPIKeyWrite(ctx, in.Name, in.KeyType, in.OwnerUserID, in.OwnerServiceAccountID, in.InstanceIDs, in.ExpiresOn, true, true, true)
	if err != nil {
		return APIKey{}, "", err
	}
	ownerUserID, ownerServiceAccountID := ownersFromNormalized(ownerUserValue, ownerSAValue)
	expiresOn := expiresOnString(expiresOnValue)
	return s.insertAPIKey(ctx, name, keyType, ownerUserID, ownerServiceAccountID, instanceIDs, expiresOn, in.CreatedByUserID)
}

func ownersFromNormalized(ownerUserValue, ownerSAValue any) (*int64, string) {
	var ownerUserID *int64
	if ownerUserValue != nil {
		if parsed, ok := ownerUserValue.(*int64); ok {
			ownerUserID = parsed
		}
	}
	ownerServiceAccountID := ""
	if ownerSAValue != nil {
		ownerServiceAccountID, _ = ownerSAValue.(string)
	}
	return ownerUserID, ownerServiceAccountID
}

func expiresOnString(value any) string {
	if value == nil {
		return ""
	}
	if parsed, ok := value.(string); ok {
		return parsed
	}
	return fmt.Sprint(value)
}

func (s *Service) insertAPIKey(ctx context.Context, name, keyType string, ownerUserID *int64, ownerServiceAccountID string, instanceIDs []string, expiresOn string, createdByUserID *int64) (APIKey, string, error) {
	secret, prefix, err := generateAPIKeySecret()
	if err != nil {
		return APIKey{}, "", err
	}
	id, err := randomToken(12)
	if err != nil {
		return APIKey{}, "", err
	}
	if err := s.apiKeys.Insert(ctx, apiKeyInsert{
		ID: id, Name: name, Prefix: prefix, TokenHash: tokenHash(secret), KeyType: keyType,
		OwnerUserID: ownerUserID, OwnerServiceAccountID: ownerServiceAccountID, InstanceIDs: instanceIDs,
		ExpiresOn: expiresOn, CreatedByUserID: createdByUserID, CreatedAt: time.Now().Unix(),
	}); err != nil {
		return APIKey{}, "", err
	}
	item, err := s.getAPIKeyIncludingHidden(ctx, id)
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
	items, err := s.apiKeys.List(ctx, serviceAccountID)
	if err != nil {
		return nil, err
	}
	liveIDs, err := s.liveInstanceIDs(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		decorateAPIKey(&items[i], liveIDs)
	}
	return items, nil
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
		retfunc (s *Service) SetAPIKeyEnabled(ctx context.Context, id string, enabled bool) error {
	if err := s.apiKeys.SetEnabled(ctx, id, enabled); err != nil {
		return err
	}
	s.clearAPIKeyCache()
	return nil
}

func (s *Service) UpdateAPIKey(ctx context.Context, id string, in UpdateAPIKeyInput) error {
	existing, err := s.getAPIKey(ctx, id)
	if err != nil {
		return err
	}
	if existing.Managed {
		if in.Name != nil && strings.TrimSpace(*in.Name) != existing.Name {
			return ErrManagedAPIKeyImmutable
		}
		if in.OwnerUserID != nil || in.OwnerServiceAccountID != nil {
			sameUser := existing.OwnerKind == OwnerKindUser && in.OwnerUserID != nil && strconv.FormatInt(*in.OwnerUserID, 10) == existing.OwnerID
			sameSA := existing.OwnerKind == OwnerKindServiceAccount && in.OwnerServiceAccountID != nil && *in.OwnerServiceAccountID == existing.OwnerID
			if !sameUser && !sameSA {
				return ErrManagedAPIKeyImmutable
			}
			in.OwnerUserID = nil
			in.OwnerServiceAccountID = nil
		}
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
	if ownerChanged {
		_, ownerSA := ownersFromNormalized(ownerUserValue, ownerSAValue)
		if err := s.rejectHiddenOrdinaryAPIKeyOwner(ctx, ownerSA); err != nil {
			return err
		}
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
	ownerUserID, ownerServiceAccountID = ownersFromNormalized(ownerUserValue, ownerSAValue)
	expiresString := expiresOnString(expiresOnValue)
	if err := s.apiKeys.Update(ctx, id, apiKeyUpdate{
		Name: name, OwnerUserID: ownerUserID, OwnerServiceAccountID: ownerServiceAccountID,
		InstanceIDs: instanceIDs, ExpiresOn: expiresString, Enabled: enabled != 0,
	}); err != nil {
		return err
	}
	s.clearAPIKeyCache()
	return nil
}

func (s *Service) RotateAPIKey(ctx context.Context, id string) (APIKey, string, error) {
	existing, err := s.getAPIKey(ctx, id)
	if err != nil {
		return APIKey{}, "", err
	}
	if existing.Managed {
		return APIKey{}, "", sql.ErrNoRows
	}
	return s.rotateAPIKeySecret(ctx, id)
}

func (s *Service) RotateManagedAPIKey(ctx context.Context, id string) (APIKey, string, error) {
	existing, err := s.getAPIKeyIncludingHidden(ctx, id)
	if err != nil {
		return APIKey{}, "", err
	}
	if !existing.Managed {
		return APIKey{}, "", sql.ErrNoRows
	}
	return s.rotateAPIKeySecret(ctx, id)
}

func (s *Service) rotateAPIKeySecret(ctx context.Context, id string) (APIKey, string, error) {
	secret, prefix, err := generateAPIKeySecret()
	if err != nil {
		return APIKey{}, "", err
	}
	if err := s.apiKeys.RotateSecret(ctx, id, prefix, tokenHash(secret)); err != nil {
		return APIKey{}, "", err
	}
	s.clearAPIUseWrite(id)
	s.clearAPIKeyCache()
	item, err := s.getAPIKeyIncludingHidden(ctx, id)
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
	hash := tokenHash(token)
	for {
		item, generation, cached := s.cachedAPIKey(hash)
		if !cached {
			var err error
			item, err = s.lookupAPIKeyByHash(ctx, hash)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return APIKey{}, ErrAPIKeyInvalid
				}
				return APIKey{}, err
			}
			if !s.rememberAPIKey(hash, item, generation) {
				continue
			}
			s.seedAPIUseWrite(item.ID, item.LastUsedAt)
		}
		if !item.Enabled || !item.OwnerEnabled || apiKeyExpired(item.ExpiresOn, time.Now().UTC()) {
			return APIKey{}, ErrAPIKeyInvalid
		}

		now := time.Now()
		stamped, ok := s.stampCachedAPIKey(hash, now.Unix(), generation)
		if !ok {
			continue
		}
		item = stamped
		if !s.reserveAPIUseWrite(item.ID, now) {
			return item, nil
		}
		if err := s.apiKeys.TouchLastUsed(ctx, item.ID, now.Unix()); err != nil {
			s.releaseAPIUseWrite(item.ID, now)
			return APIKey{}, err
		}
		return item, nil
	}
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

func (s *Service) getAPIKey(ctx context.Context, id string) (APIKey, error) {
	return s.getAPIKeyIncludingHidden(ctx, id)
}

func (s *Service) getAPIKeyIncludingHidden(ctx context.Context, id string) (APIKey, error) {
	item, err := s.apiKeys.GetByID(ctx, id)
	if err != nil {
		return APIKey{}, err
	}
	liveIDs, err := s.liveInstanceIDs(ctx)
	if err != nil {
		return APIKey{}, err
	}
	decorateAPIKey(&item, liveIDs)
	return item, nil
}

func (s *Service) lookupAPIKeyByHash(ctx context.Context, hash string) (APIKey, error) {
	item, err := s.apiKeys.GetByHash(ctx, hash)
	if err != nil {
		return APIKey{}, err
	}
	liveIDs, err := s.liveInstanceIDs(ctx)
	if err != nil {
		return APIKey{}, err
	}
	decorateAPIKey(&item, liveIDs)
	return item, nil
}

func decorateAPIKey(item *APIKey, liveIDs map[string]struct{}) {
	item.MissingInstanceIDs = missingInstanceIDs(item.InstanceIDs, liveIDs)
	item.Status = computeAPIKeyStatus(item.Enabled, item.OwnerEnabled, item.ExpiresOn, time.Now().UTC())
	if item.InstanceIDs == nil {
		item.InstanceIDs = []string{}
	}
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
	enabled, err := s.apiKeys.OwnerEnabled(ctx, ownerUserID, ownerServiceAccountID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return "", "", nil, nil, nil, nil, ErrAPIKeyOwnerNotFound
		}
		return "", "", nil, nil, nil, nil, err
	}
	if validateOwnerEnabled && !enabled {
		return "", "", nil, nil, nil, nil, ErrAPIKeyOwnerDisabled
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
	return s.apiKeys.LiveInstanceIDs(ctx)
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

func (s *Service) serviceAccountHidden(ctx context.Context, id string) (bool, error) {
	hidden, err := s.apiKeys.ServiceAccountHidden(ctx, strings.TrimSpace(id))
	if errors.Is(err, database.ErrNotFound) {
		return false, ErrAPIKeyOwnerNotFound
	}
	return hidden, err
}

// rejectHiddenOrdinaryAPIKeyOwner rejects hidden service-account owners on
// generic/public API-key write paths. Missing accounts already fail in
// normalizeAPIKeyWrite as ErrAPIKeyOwnerNotFound. Hidden accounts that exist
// return sql.ErrNoRows so HTTP maps to 404 "api key not found" without
// disclosing the hidden principal.
func (s *Service) rejectHiddenOrdinaryAPIKeyOwner(ctx context.Context, ownerServiceAccountID string) error {
	if strings.TrimSpace(ownerServiceAccountID) == "" {
		return nil
	}
	hidden, err := s.serviceAccountHidden(ctx, ownerServiceAccountID)
	if err != nil {
		return err
	}
	if hidden {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Service) EnsureManagedInferenceKey(ctx context.Context, serviceAccountID string) (APIKey, string, error) {
	keys, err := s.listAPIKeysIncludingHidden(ctx, serviceAccountID)
	if err != nil {
		return APIKey{}, "", err
	}
	for _, key := range keys {
		if key.Name == ManagedPrincipalName && key.KeyType == APIKeyTypeInference {
			return key, "", nil
		}
	}
	return s.createAPIKey(ctx, CreateAPIKeyInput{
		Name:                  ManagedPrincipalName,
		KeyType:               APIKeyTypeInference,
		OwnerServiceAccountID: serviceAccountID,
		InstanceIDs:           []string{},
	})
}

func (s *Service) ManagedInferenceKey(ctx context.Context) (APIKey, error) {
	account, err := s.FindHiddenServiceAccountByName(ctx, ManagedPrincipalName)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, sql.ErrNoRows
	}
	if err != nil {
		return APIKey{}, err
	}
	keys, err := s.listAPIKeysIncludingHidden(ctx, account.ID)
	if err != nil {
		return APIKey{}, err
	}
	for _, key := range keys {
		if key.Name == ManagedPrincipalName && key.KeyType == APIKeyTypeInference {
			return key, nil
		}
	}
	return APIKey{}, sql.ErrNoRows
}

func (s *Service) listAPIKeysIncludingHidden(ctx context.Context, serviceAccountID string) ([]APIKey, error) {
	return s.listAPIKeys(ctx, serviceAccountID)
}
