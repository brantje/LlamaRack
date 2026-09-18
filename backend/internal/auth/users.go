package auth

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (s *Service) BootstrapRequired(ctx context.Context) (bool, error) {
	return s.users.BootstrapRequired(ctx)
}

func (s *Service) Bootstrap(ctx context.Context, username, password string) (User, error) {
	username, err := validateCredentials(username, password)
	if err != nil {
		return User{}, err
	}
	hash, err := hashPasswordContext(ctx, password)
	if err != nil {
		return User{}, err
	}
	return s.users.Bootstrap(ctx, username, hash, time.Now().Unix())
}

func (s *Service) CreateUser(ctx context.Context, username, password string) (User, error) {
	username, err := validateCredentials(username, password)
	if err != nil {
		return User{}, err
	}
	hash, err := hashPasswordContext(ctx, password)
	if err != nil {
		return User{}, err
	}
	return s.users.Create(ctx, username, hash, time.Now().Unix())
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	return s.users.List(ctx, time.Now().Unix())
}

func (s *Service) UserByID(ctx context.Context, id int64) (User, error) {
	return s.users.ByID(ctx, id)
}

func (s *Service) SetUserEnabled(ctx context.Context, id int64, enabled bool) error {
	if err := s.users.SetEnabled(ctx, id, enabled); err != nil {
		return err
	}
	s.clearAPIKeyCache()
	return nil
}

func (s *Service) DeleteUser(ctx context.Context, actorID, id int64) error {
	if actorID == id {
		return ErrSelfDelete
	}
	if err := s.users.Delete(ctx, id); err != nil {
		return err
	}
	s.clearAPIKeyCache()
	return nil
}

func (s *Service) ResetPassword(ctx context.Context, userID int64, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hash, err := hashPasswordContext(ctx, newPassword)
	if err != nil {
		return err
	}
	return s.users.ResetPassword(ctx, userID, hash)
}

func (s *Service) ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword, keepSessionID string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	currentHash, err := s.users.EnabledPasswordHash(ctx, userID)
	if err != nil {
		return ErrInvalidCredentials
	}
	verified, err := verifyPasswordContext(ctx, currentPassword, currentHash)
	if err != nil {
		return err
	}
	if !verified {
		return ErrInvalidCredentials
	}
	newHash, err := hashPasswordContext(ctx, newPassword)
	if err != nil {
		return err
	}
	return s.users.ChangePassword(ctx, userID, newHash, keepSessionID)
}

func validateCredentials(username, password string) (string, error) {
	username = strings.TrimSpace(username)
	if len(username) < 2 {
		return "", errors.New("username must be at least 2 characters")
	}
	if err := validatePassword(password); err != nil {
		return "", err
	}
	return username, nil
}
