package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
)

func (s *Service) CreateServiceAccount(ctx context.Context, name string, createdByUserID int64) (ServiceAccount, error) {
	return s.createServiceAccount(ctx, name, createdByUserID, false)
}

func (s *Service) EnsureHiddenServiceAccount(ctx context.Context, name string) (ServiceAccount, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ServiceAccount{}, ErrServiceAccountNameRequired
	}
	existing, err := s.findServiceAccountByName(ctx, name, true)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, database.ErrNotFound) {
		return ServiceAccount{}, err
	}
	return s.createServiceAccount(ctx, name, 0, true)
}

func (s *Service) createServiceAccount(ctx context.Context, name string, createdByUserID int64, hidden bool) (ServiceAccount, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ServiceAccount{}, ErrServiceAccountNameRequired
	}
	id, err := randomToken(12)
	if err != nil {
		return ServiceAccount{}, err
	}
	item := ServiceAccount{ID: id, Name: name, Enabled: true, Hidden: hidden, CreatedAt: time.Now().Unix()}
	if createdByUserID > 0 {
		value := createdByUserID
		item.CreatedByUserID = &value
	}
	if err := s.serviceAccounts.Create(ctx, item); err != nil {
		return ServiceAccount{}, err
	}
	return item, nil
}

func (s *Service) ListServiceAccounts(ctx context.Context) ([]ServiceAccount, error) {
	return s.serviceAccounts.ListVisible(ctx)
}

func (s *Service) GetServiceAccount(ctx context.Context, id string) (ServiceAccount, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ServiceAccount{}, database.ClassifyError(sql.ErrNoRows)
	}
	item, err := s.serviceAccounts.Get(ctx, id)
	if err != nil {
		return ServiceAccount{}, err
	}
	if item.Hidden {
		return ServiceAccount{}, database.ClassifyError(sql.ErrNoRows)
	}
	keys, err := s.ListAPIKeysForServiceAccount(ctx, id)
	if err != nil {
		return ServiceAccount{}, err
	}
	item.Keys = keys
	return item, nil
}

func (s *Service) FindHiddenServiceAccountByName(ctx context.Context, name string) (ServiceAccount, error) {
	return s.findServiceAccountByName(ctx, name, true)
}

func (s *Service) findServiceAccountByName(ctx context.Context, name string, hiddenOnly bool) (ServiceAccount, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ServiceAccount{}, database.ClassifyError(sql.ErrNoRows)
	}
	return s.serviceAccounts.FindByName(ctx, name, hiddenOnly)
}

func (s *Service) DeleteHiddenServiceAccountByName(ctx context.Context, name string) error {
	account, err := s.findServiceAccountByName(ctx, name, true)
	if errors.Is(err, database.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.deleteServiceAccount(ctx, account.ID)
}

func (s *Service) UpdateServiceAccount(ctx context.Context, id string, name *string, enabled *bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return database.ClassifyError(sql.ErrNoRows)
	}
	existing, err := s.serviceAccounts.Get(ctx, id)
	if err != nil {
		return err
	}
	if existing.Hidden {
		return database.ClassifyError(sql.ErrNoRows)
	}
	nextName := existing.Name
	if name != nil {
		nextName = strings.TrimSpace(*name)
		if nextName == "" {
			return ErrServiceAccountNameRequired
		}
	}
	nextEnabled := existing.Enabled
	if enabled != nil {
		nextEnabled = *enabled
	}
	if err := s.serviceAccounts.Update(ctx, id, nextName, nextEnabled); err != nil {
		return err
	}
	s.clearAPIKeyCache()
	return nil
}

func (s *Service) DeleteServiceAccount(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return database.ClassifyError(sql.ErrNoRows)
	}
	existing, err := s.serviceAccounts.Get(ctx, id)
	if err != nil {
		return err
	}
	if existing.Hidden {
		return database.ClassifyError(sql.ErrNoRows)
	}
	return s.deleteServiceAccount(ctx, id)
}

func (s *Service) deleteServiceAccount(ctx context.Context, id string) error {
	if err := s.serviceAccounts.Delete(ctx, id); err != nil {
		return err
	}
	s.clearAPIKeyCache()
	return nil
}
