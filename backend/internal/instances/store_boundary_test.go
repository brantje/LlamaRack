package instances

import (
	"context"
	"testing"
)

type fakeInstanceStore struct {
	item Instance
}

func (f *fakeInstanceStore) RequireModel(context.Context, string) error { return nil }
func (f *fakeInstanceStore) Create(context.Context, Instance, map[string]string) error { return nil }
func (f *fakeInstanceStore) Update(context.Context, string, Instance, map[string]string, bool) error { return nil }
func (f *fakeInstanceStore) GetByID(context.Context, string) (Instance, error) { return f.item, nil }
func (f *fakeInstanceStore) GetBySlug(context.Context, string) (Instance, error) { return f.item, nil }
func (f *fakeInstanceStore) List(context.Context) ([]Instance, error) { return []Instance{f.item}, nil }
func (f *fakeInstanceStore) ListByModel(context.Context, string) ([]Instance, error) { return []Instance{f.item}, nil }
func (f *fakeInstanceStore) Options(context.Context, string) (map[string]string, error) { return map[string]string{}, nil }
func (f *fakeInstanceStore) Delete(context.Context, string) error { return nil }

func TestServiceUsesDomainInstanceStoreWithoutDatabaseStore(t *testing.T) {
	fake := &fakeInstanceStore{item: Instance{ID: "instance-1", Slug: "fake-instance", ModelID: "model-1", Name: "Fake"}}
	service := NewWithStore(fake)
	got, err := service.GetByID(context.Background(), fake.item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != fake.item.ID || got.Slug != fake.item.Slug {
		t.Fatalf("instance=%+v", got)
	}
}
