package litellm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/modelimports"
	"github.com/brantje/llamarack/backend/internal/settings"
)

type LastSync struct {
	At          string `json:"at"`
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
	Published   int    `json:"published"`
	Unpublished int    `json:"unpublished"`
}

type Status struct {
	ProxyURL       string    `json:"proxy_url"`
	APIBase        string    `json:"api_base"`
	DefaultAPIBase string    `json:"default_api_base"`
	ProxyKey       KeyStatus `json:"proxy_key"`
	GeneratedKey   KeyStatus `json:"generated_key"`
	LastSync       *LastSync `json:"last_sync,omitempty"`
	Configured     bool      `json:"configured"`
	LastSyncOK     bool      `json:"last_sync_ok"`
}

type KeyStatus struct {
	Configured bool   `json:"configured"`
	Prefix     string `json:"prefix,omitempty"`
	Name       string `json:"name,omitempty"`
}

type SaveInput struct {
	ProxyURL string `json:"proxy_url"`
	APIBase  string `json:"api_base"`
	ProxyKey string `json:"proxy_key"`
}

type DisconnectInput struct {
	Unpublish bool `json:"unpublish"`
}

type Service struct {
	db       *sql.DB
	auth     *auth.Service
	secrets  *huggingface.SecretStore
	settings *settings.Service
	http     *http.Client

	reconcileMu sync.Mutex
}

func New(db *sql.DB, authService *auth.Service, secrets *huggingface.SecretStore, managerSettings *settings.Service) *Service {
	return &Service{db: db, auth: authService, secrets: secrets, settings: managerSettings, http: &http.Client{Timeout: 30 * time.Second}}
}

func (s *Service) SetHTTPClient(client *http.Client) {
	if client != nil {
		s.http = client
	}
}

func (s *Service) NotifyInstanceChange(ctx context.Context, instanceID string) {
	_ = instanceID
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := s.Reconcile(bg); err != nil {
			slog.Warn("litellm reconcile after instance change failed", "error", err)
		}
	}()
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	proxyURL, _ := s.getSetting(ctx, SettingProxyURL)
	apiBase, _ := s.getSetting(ctx, SettingAPIBase)
	defaultBase, err := s.defaultAPIBase(ctx)
	if err != nil {
		return Status{}, err
	}
	proxyStatus, err := s.secrets.SecretStatus(ctx, SecretProxyAPIKey)
	if err != nil {
		return Status{}, err
	}
	lastSync, _ := s.loadLastSync(ctx)
	status := Status{
		ProxyURL:       proxyURL,
		APIBase:        apiBase,
		DefaultAPIBase: defaultBase,
		ProxyKey:       KeyStatus{Configured: proxyStatus.Configured, Prefix: proxyStatus.Prefix},
		Configured:     strings.TrimSpace(proxyURL) != "" && proxyStatus.Configured,
	}
	if key, err := s.auth.ManagedInferenceKey(ctx); err == nil {
		status.GeneratedKey = KeyStatus{Configured: true, Prefix: key.Prefix, Name: key.Name}
	}
	if lastSync != nil {
		status.LastSync = lastSync
		status.LastSyncOK = lastSync.OK
	}
	return status, nil
}

func (s *Service) Save(ctx context.Context, in SaveInput) (Status, error) {
	proxyURL := strings.TrimSpace(in.ProxyURL)
	if proxyURL == "" {
		return Status{}, errors.New("proxy URL is required")
	}
	apiBase := strings.TrimSpace(in.APIBase)
	if apiBase == "" {
		var err error
		apiBase, err = s.defaultAPIBase(ctx)
		if err != nil {
			return Status{}, err
		}
	}
	apiBase = strings.TrimRight(apiBase, "/")
	if err := s.setSetting(ctx, SettingProxyURL, proxyURL); err != nil {
		return Status{}, err
	}
	if err := s.setSetting(ctx, SettingAPIBase, apiBase); err != nil {
		return Status{}, err
	}
	if strings.TrimSpace(in.ProxyKey) != "" {
		if err := s.secrets.SetSecretWithPrefix(ctx, SecretProxyAPIKey, in.ProxyKey); err != nil {
			return Status{}, err
		}
	}
	configured, err := s.secrets.SecretConfigured(ctx, SecretProxyAPIKey)
	if err != nil {
		return Status{}, err
	}
	if !configured {
		return Status{}, errors.New("LiteLLM proxy API key is required")
	}
	if err := s.ensurePrincipal(ctx); err != nil {
		return Status{}, err
	}
	client, err := s.newClient(ctx)
	if err != nil {
		return Status{}, err
	}
	if err := client.TestConnection(ctx); err != nil {
		_ = s.persistLastSync(ctx, LastSync{At: time.Now().UTC().Format(time.RFC3339), OK: false, Error: err.Error()})
		return Status{}, err
	}
	if _, err := s.Reconcile(ctx); err != nil {
		return Status{}, err
	}
	return s.Status(ctx)
}

func (s *Service) Test(ctx context.Context) error {
	client, err := s.newClient(ctx)
	if err != nil {
		return err
	}
	return client.TestConnection(ctx)
}

func (s *Service) Reconcile(ctx context.Context) (LastSync, error) {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	return s.reconcile(ctx)
}

func (s *Service) Rotate(ctx context.Context) (Status, error) {
	key, err := s.auth.ManagedInferenceKey(ctx)
	if err != nil {
		return Status{}, err
	}
	rotated, secret, err := s.auth.RotateManagedAPIKey(ctx, key.ID)
	if err != nil {
		return Status{}, err
	}
	if err := s.secrets.SetSecret(ctx, SecretInferenceAPIKey, secret); err != nil {
		return Status{}, err
	}
	_ = rotated
	if _, err := s.Reconcile(ctx); err != nil {
		return Status{}, err
	}
	return s.Status(ctx)
}

func (s *Service) Disconnect(ctx context.Context, in DisconnectInput) error {
	if in.Unpublish {
		if _, err := s.unpublishAll(ctx); err != nil {
			return err
		}
	}
	if err := s.auth.DeleteHiddenServiceAccountByName(ctx, auth.ManagedPrincipalName); err != nil {
		return err
	}
	for _, name := range []string{SecretProxyAPIKey, SecretInferenceAPIKey} {
		if err := s.secrets.DeleteSecret(ctx, name); err != nil {
			return err
		}
	}
	for _, key := range []string{SettingProxyURL, SettingAPIBase, SettingLastSync} {
		if err := s.deleteSetting(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) BootReconcile(ctx context.Context) {
	proxyURL, _ := s.getSetting(ctx, SettingProxyURL)
	configured, err := s.secrets.SecretConfigured(ctx, SecretProxyAPIKey)
	if err != nil || strings.TrimSpace(proxyURL) == "" || !configured {
		return
	}
	if _, err := s.Reconcile(ctx); err != nil {
		slog.Warn("litellm boot reconcile failed", "error", err)
	}
}

func (s *Service) reconcile(ctx context.Context) (LastSync, error) {
	client, err := s.newClient(ctx)
	if err != nil {
		return s.reconcileFailure(ctx, err, 0, 0)
	}
	apiBase, err := s.effectiveAPIBase(ctx)
	if err != nil {
		return s.reconcileFailure(ctx, err, 0, 0)
	}
	inferenceKey, err := s.inferenceSecret(ctx)
	if err != nil {
		return s.reconcileFailure(ctx, err, 0, 0)
	}
	enabled, err := s.enabledInstances(ctx)
	if err != nil {
		return s.reconcileFailure(ctx, err, 0, 0)
	}
	enabledByID := make(map[string]instances.Instance, len(enabled))
	enabledBySlug := make(map[string]instances.Instance, len(enabled))
	for _, item := range enabled {
		enabledByID[item.ID] = item
		enabledBySlug[item.Slug] = item
	}

	remote, err := client.ListModels(ctx)
	if err != nil {
		return s.reconcileFailure(ctx, err, 0, 0)
	}
	owned := make([]ModelEntry, 0)
	ownedByInstanceID := make(map[string]ModelEntry)
	for _, entry := range remote {
		if !IsManaged(entry) {
			continue
		}
		owned = append(owned, entry)
		owner := strings.TrimSpace(entry.ModelInfo.LlamaRackInstanceID)
		if _, ok := enabledByID[owner]; ok {
			ownedByInstanceID[owner] = entry
			continue
		}
		// Pre-#180 rows stored the public Instance slug in durable ownership
		// metadata. Adopt those rows by mapping either the legacy metadata value or
		// model_name to the current Instance UUID, then update them in place below.
		if item, ok := enabledBySlug[owner]; ok {
			ownedByInstanceID[item.ID] = entry
			continue
		}
		if item, ok := enabledBySlug[strings.TrimSpace(entry.ModelName)]; ok {
			if _, exists := ownedByInstanceID[item.ID]; !exists {
				ownedByInstanceID[item.ID] = entry
			}
		}
	}

	published := 0
	for id, item := range enabledByID {
		entry, ok := ownedByInstanceID[id]
		if !ok {
			model := BuildInstanceModelEntry(item.ID, item.Slug, apiBase, inferenceKey, "")
			if err := client.CreateModel(ctx, model); err != nil {
				return s.reconcileFailure(ctx, err, published, 0)
			}
			published++
			continue
		}
		if instanceEntryDrifted(entry, item.ID, item.Slug, apiBase, inferenceKey) {
			update := BuildInstanceModelEntry(item.ID, item.Slug, apiBase, inferenceKey, entry.ModelInfo.ID)
			if err := client.UpdateModel(ctx, update); err != nil {
				return s.reconcileFailure(ctx, err, published, 0)
			}
			published++
		}
	}

	unpublished := 0
	for _, entry := range owned {
		if _, ok := resolveManagedRemoteInstance(entry, enabledByID, enabledBySlug); ok {
			continue
		}
		if entry.ModelInfo.ID == "" {
			continue
		}
		if err := client.DeleteModel(ctx, entry.ModelInfo.ID); err != nil {
			return s.reconcileFailure(ctx, err, published, unpublished)
		}
		unpublished++
	}
	syncResult := LastSync{At: time.Now().UTC().Format(time.RFC3339), OK: true, Published: published, Unpublished: unpublished}
	_ = s.persistLastSync(ctx, syncResult)
	return syncResult, nil
}

func resolveManagedRemoteInstance(entry ModelEntry, byID, bySlug map[string]instances.Instance) (instances.Instance, bool) {
	owner := strings.TrimSpace(entry.ModelInfo.LlamaRackInstanceID)
	if item, ok := byID[owner]; ok {
		return item, true
	}
	if item, ok := bySlug[owner]; ok {
		return item, true
	}
	item, ok := bySlug[strings.TrimSpace(entry.ModelName)]
	return item, ok
}

func (s *Service) reconcileFailure(ctx context.Context, err error, published, unpublished int) (LastSync, error) {
	syncResult := LastSync{At: time.Now().UTC().Format(time.RFC3339), OK: false, Error: err.Error(), Published: published, Unpublished: unpublished}
	_ = s.persistLastSync(ctx, syncResult)
	return syncResult, err
}

func (s *Service) unpublishAll(ctx context.Context) (int, error) {
	client, err := s.newClient(ctx)
	if err != nil {
		return 0, err
	}
	remote, err := client.ListModels(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range remote {
		if !IsManaged(entry) || entry.ModelInfo.ID == "" {
			continue
		}
		if err := client.DeleteModel(ctx, entry.ModelInfo.ID); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (s *Service) ensurePrincipal(ctx context.Context) error {
	account, err := s.auth.EnsureHiddenServiceAccount(ctx, auth.ManagedPrincipalName)
	if err != nil {
		return err
	}
	key, secret, err := s.auth.EnsureManagedInferenceKey(ctx, account.ID)
	if err != nil {
		return err
	}
	if secret != "" {
		return s.secrets.SetSecret(ctx, SecretInferenceAPIKey, secret)
	}
	configured, err := s.secrets.SecretConfigured(ctx, SecretInferenceAPIKey)
	if err != nil {
		return err
	}
	if configured {
		return nil
	}
	_, secret, err = s.auth.RotateManagedAPIKey(ctx, key.ID)
	if err != nil {
		return err
	}
	return s.secrets.SetSecret(ctx, SecretInferenceAPIKey, secret)
}

func (s *Service) inferenceSecret(ctx context.Context) (string, error) {
	if err := s.ensurePrincipal(ctx); err != nil {
		return "", err
	}
	secret, err := s.secrets.GetSecret(ctx, SecretInferenceAPIKey)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(secret) == "" {
		return "", errors.New("managed inference API key is not configured")
	}
	return secret, nil
}

func (s *Service) newClient(ctx context.Context) (*Client, error) {
	proxyURL, err := s.getSetting(ctx, SettingProxyURL)
	if err != nil {
		return nil, err
	}
	apiKey, err := s.secrets.GetSecret(ctx, SecretProxyAPIKey)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("LiteLLM proxy API key is not configured")
	}
	return NewClient(proxyURL, apiKey, s.http)
}

func (s *Service) enabledInstances(ctx context.Context) ([]instances.Instance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,slug,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds,max_pending_requests,gpu_mode,gpu_devices,tensor_split,request_log_mode FROM instances WHERE enabled=1 AND NOT EXISTS (SELECT 1 FROM provider_imports pi WHERE pi.instance_id=instances.id AND pi.state=?) ORDER BY name,id`, modelimports.StateDownloading)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]instances.Instance, 0)
	for rows.Next() {
		var item instances.Instance
		var enabled, autoload, alwaysOn, eviction int
		var devices, split sql.NullString
		if err := rows.Scan(&item.ID, &item.Slug, &item.ModelID, &item.Name, &enabled, &autoload, &alwaysOn, &item.Priority, &eviction, &item.IdleUnloadSeconds, &item.MaxPendingRequests, &item.GPUMode, &devices, &split, &item.RequestLogMode); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		item.Autoload = autoload != 0
		item.AlwaysOn = alwaysOn != 0
		item.EvictionEnabled = eviction != 0
		if devices.Valid && strings.TrimSpace(devices.String) != "" {
			for _, device := range strings.Split(devices.String, ",") {
				if device = strings.TrimSpace(device); device != "" {
					item.GPUDevices = append(item.GPUDevices, device)
				}
			}
		}
		if split.Valid {
			item.TensorSplit = split.String
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) defaultAPIBase(ctx context.Context) (string, error) {
	external, err := s.settings.String(ctx, settings.ExternalURL)
	if err != nil {
		return "", err
	}
	external = strings.TrimRight(strings.TrimSpace(external), "/")
	if external == "" {
		return "", nil
	}
	return external + "/v1", nil
}

func (s *Service) effectiveAPIBase(ctx context.Context) (string, error) {
	apiBase, err := s.getSetting(ctx, SettingAPIBase)
	if err != nil {
		return "", err
	}
	apiBase = strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if apiBase != "" {
		return apiBase, nil
	}
	return s.defaultAPIBase(ctx)
}

func (s *Service) getSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT setting_value FROM manager_settings WHERE setting_key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (s *Service) setSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)
		ON CONFLICT(setting_key) DO UPDATE SET setting_value=excluded.setting_value,updated_at=excluded.updated_at`, key, value, time.Now().Unix())
	return err
}

func (s *Service) deleteSetting(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM manager_settings WHERE setting_key=?", key)
	return err
}

func (s *Service) persistLastSync(ctx context.Context, syncResult LastSync) error {
	data, err := json.Marshal(syncResult)
	if err != nil {
		return err
	}
	return s.setSetting(ctx, SettingLastSync, string(data))
}

func (s *Service) loadLastSync(ctx context.Context) (*LastSync, error) {
	raw, err := s.getSetting(ctx, SettingLastSync)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, err
	}
	var syncResult LastSync
	if err := json.Unmarshal([]byte(raw), &syncResult); err != nil {
		return nil, fmt.Errorf("decode litellm last sync: %w", err)
	}
	return &syncResult, nil
}
