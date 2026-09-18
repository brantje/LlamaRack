package settings

import (
	"context"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
)

type fakeSettingStore struct {
	values map[string]string
}

func (f *fakeSettingStore) Get(_ context.Context, key string) (string, error) {
	value, ok := f.values[key]
	if !ok {
		return "", database.ErrNotFound
	}
	return value, nil
}

func (f *fakeSettingStore) Set(_ context.Context, key, value string, _ int64) error {
	f.values[key] = value
	return nil
}

func TestServiceUsesDomainSettingStoreWithoutDatabaseStore(t *testing.T) {
	fake := &fakeSettingStore{values: map[string]string{IdleUnloadSeconds: "42"}}
	service := NewWithStore(fake, Defaults{})
	value, err := service.Resolve(context.Background(), IdleUnloadSeconds)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := value.Value.(int); !ok || got != 42 {
		t.Fatalf("resolved value=%#v", value.Value)
	}
}
