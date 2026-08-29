package settings

import "testing"

func TestFrontendURLDefaultsEmptyAndIsConfigurable(t *testing.T) {
	s := testSettings(t)
	ctx := t.Context()

	value, err := s.Resolve(ctx, FrontendURL)
	if err != nil {
		t.Fatal(err)
	}
	if value.Value != "" || value.Source != "default" || !value.Editable {
		t.Fatalf("frontend URL default=%+v", value)
	}

	if _, err := s.Set(ctx, FrontendURL, "http://192.168.60.5:3000"); err != nil {
		t.Fatal(err)
	}
	got, err := s.String(ctx, FrontendURL)
	if err != nil || got != "http://192.168.60.5:3000" {
		t.Fatalf("frontend URL=%q err=%v", got, err)
	}

	general, err := s.General(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if general.FrontendURL.Value != "http://192.168.60.5:3000" || general.FrontendURL.Source != "database" {
		t.Fatalf("general frontend URL=%+v", general.FrontendURL)
	}
}
