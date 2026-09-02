package huggingface

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
)

func (s *SecretStore) GetSecret(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("secret name is required")
	}
	var ciphertext, nonce []byte
	err := s.db.QueryRowContext(ctx, "SELECT ciphertext,nonce FROM provider_secrets WHERE name=?", name).Scan(&ciphertext, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	plain, err := s.aead.Open(nil, nonce, ciphertext, []byte(name))
	if err != nil {
		return "", fmt.Errorf("decrypt provider secret: %w", err)
	}
	return string(plain), nil
}

// SetSecret stores generic provider credentials as opaque secrets. Display
// prefixes are intentionally reserved for token-specific storage such as
// SetToken, where the prefix is part of the product UX.
func (s *SecretStore) SetSecret(ctx context.Context, name, value string) error {
	return s.setSecret(ctx, name, value, "")
}

func (s *SecretStore) SetSecretWithPrefix(ctx context.Context, name, value string) error {
	value = strings.TrimSpace(value)
	prefix := value
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return s.setSecret(ctx, name, value, prefix)
}

func (s *SecretStore) setSecret(ctx context.Context, name, value, prefix string) error {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if name == "" || value == "" {
		return errors.New("secret name and value are required")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	ciphertext := s.aead.Seal(nil, nonce, []byte(value), []byte(name))
	_, err := s.db.ExecContext(ctx, `INSERT INTO provider_secrets(name,ciphertext,nonce,prefix,updated_at)
VALUES(?,?,?,?,unixepoch())
ON CONFLICT(name) DO UPDATE SET ciphertext=excluded.ciphertext,nonce=excluded.nonce,prefix=excluded.prefix,updated_at=unixepoch()`, name, ciphertext, nonce, prefix)
	return err
}

func (s *SecretStore) SecretStatus(ctx context.Context, name string) (TokenStatus, error) {
	var prefix string
	err := s.db.QueryRowContext(ctx, "SELECT prefix FROM provider_secrets WHERE name=?", strings.TrimSpace(name)).Scan(&prefix)
	if errors.Is(err, sql.ErrNoRows) {
		return TokenStatus{}, nil
	}
	if err != nil {
		return TokenStatus{}, err
	}
	return TokenStatus{Configured: true, Prefix: prefix}, nil
}

func (s *SecretStore) DeleteSecret(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM provider_secrets WHERE name=?", strings.TrimSpace(name))
	return err
}

func (s *SecretStore) SecretConfigured(ctx context.Context, name string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM provider_secrets WHERE name=?", strings.TrimSpace(name)).Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}
