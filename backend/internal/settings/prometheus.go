package settings

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/brantje/llamarack/backend/internal/huggingface"
)

var errProviderSecretsUnavailable = errors.New("provider secrets are unavailable")

const prometheusAuthTokenEnv = "LLAMARACK_PROMETHEUS_AUTH_TOKEN"

type SecretValue struct {
	Configured bool   `json:"configured"`
	Prefix     string `json:"prefix,omitempty"`
	Source     string `json:"source"`
	Editable   bool   `json:"editable"`
}

func envPrometheusToken() string {
	value, ok := os.LookupEnv(prometheusAuthTokenEnv)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func prometheusPrefix(value string) string {
	if len(value) > 8 {
		return value[:8]
	}
	return value
}

func PrometheusTokenStatus(ctx context.Context, store *huggingface.SecretStore) (SecretValue, error) {
	if store != nil {
		status, err := store.SecretStatus(ctx, huggingface.SecretPrometheusAuthToken)
		if err != nil {
			return SecretValue{}, err
		}
		if status.Configured {
			return SecretValue{Configured: true, Prefix: status.Prefix, Source: "database", Editable: true}, nil
		}
	}
	if env := envPrometheusToken(); env != "" {
		return SecretValue{Configured: true, Prefix: prometheusPrefix(env), Source: "environment", Editable: true}, nil
	}
	return SecretValue{Configured: false, Source: "default", Editable: true}, nil
}

func ResolvePrometheusToken(ctx context.Context, store *huggingface.SecretStore) (string, error) {
	if store != nil {
		secret, err := store.GetSecret(ctx, huggingface.SecretPrometheusAuthToken)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(secret) != "" {
			return secret, nil
		}
	}
	return envPrometheusToken(), nil
}

func SetPrometheusToken(ctx context.Context, store *huggingface.SecretStore, value string) error {
	if store == nil {
		return errProviderSecretsUnavailable
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return store.DeleteSecret(ctx, huggingface.SecretPrometheusAuthToken)
	}
	return store.SetSecretWithPrefix(ctx, huggingface.SecretPrometheusAuthToken, value)
}
