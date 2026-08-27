package huggingface

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const tokenSecretName = "huggingface_token"

type TokenStatus struct {
	Configured bool   `json:"configured"`
	Prefix     string `json:"prefix,omitempty"`
}

type SecretStore struct {
	db   *sql.DB
	aead cipher.AEAD
}

func NewSecretStore(db *sql.DB, dataDir string) (*SecretStore, error) {
	key, err := loadOrCreateKey(filepath.Join(dataDir, "provider-secrets.key"))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SecretStore{db: db, aead: aead}, nil
}

func (s *SecretStore) GetToken(ctx context.Context) (string, error) {
	var ciphertext, nonce []byte
	err := s.db.QueryRowContext(ctx, "SELECT ciphertext,nonce FROM provider_secrets WHERE name=?", tokenSecretName).Scan(&ciphertext, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	plain, err := s.aead.Open(nil, nonce, ciphertext, []byte(tokenSecretName))
	if err != nil {
		return "", fmt.Errorf("decrypt Hugging Face token: %w", err)
	}
	return string(plain), nil
}

func (s *SecretStore) SetToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("Hugging Face token is required")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	ciphertext := s.aead.Seal(nil, nonce, []byte(token), []byte(tokenSecretName))
	prefix := token
	if len(prefix) > 6 {
		prefix = prefix[:6]
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO provider_secrets(name,ciphertext,nonce,prefix,updated_at)
VALUES(?,?,?,?,unixepoch())
ON CONFLICT(name) DO UPDATE SET ciphertext=excluded.ciphertext,nonce=excluded.nonce,prefix=excluded.prefix,updated_at=unixepoch()`, tokenSecretName, ciphertext, nonce, prefix)
	return err
}

func (s *SecretStore) DeleteToken(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM provider_secrets WHERE name=?", tokenSecretName)
	return err
}

func (s *SecretStore) TokenStatus(ctx context.Context) (TokenStatus, error) {
	var prefix string
	err := s.db.QueryRowContext(ctx, "SELECT prefix FROM provider_secrets WHERE name=?", tokenSecretName).Scan(&prefix)
	if errors.Is(err, sql.ErrNoRows) {
		return TokenStatus{}, nil
	}
	if err != nil {
		return TokenStatus{}, err
	}
	return TokenStatus{Configured: true, Prefix: prefix}, nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != 32 {
			return nil, errors.New("provider secret key has invalid length")
		}
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadOrCreateKey(path)
		}
		return nil, err
	}
	if _, err := file.Write(key); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return key, nil
}
