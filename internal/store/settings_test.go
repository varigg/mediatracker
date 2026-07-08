package store

import (
	"context"
	"testing"
)

func TestSettingRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, ok, err := s.GetSetting(ctx, "steam_id"); err != nil || ok {
		t.Fatalf("GetSetting on unset key = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	if err := s.SetSetting(ctx, "steam_id", "76561190000000001"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	got, ok, err := s.GetSetting(ctx, "steam_id")
	if err != nil || !ok || got != "76561190000000001" {
		t.Fatalf("GetSetting = (%q, %v, %v), want the stored value", got, ok, err)
	}
	if err := s.SetSetting(ctx, "steam_id", "76561190000000002"); err != nil {
		t.Fatalf("SetSetting overwrite: %v", err)
	}
	got, _, err = s.GetSetting(ctx, "steam_id")
	if err != nil || got != "76561190000000002" {
		t.Fatalf("GetSetting after overwrite = %q, want updated value", got)
	}
}
