package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quakerelay/server/internal/model"
)

func TestDeviceLifecycleAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	secret := strings.Repeat("s", 32)
	code, err := s.CreatePairing(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Pair(ctx, code, "phone", secret, now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Pair(ctx, code, "phone", secret, now)
	if err != nil || again != token {
		t.Fatal("pair replay", err)
	}
	if _, err = s.Pair(ctx, code, "other", secret, now); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("cross-installation pairing")
	}
	id, err := s.Authenticate(ctx, token)
	if err != nil || id != "phone" {
		t.Fatal(err)
	}
	r := model.Registration{InstallationID: id, DeviceToken: "aabb", Environment: "development"}
	if err = s.Register(ctx, id, r, now); err != nil {
		t.Fatal(err)
	}
	old, _ := s.Device(ctx, id)
	r.DeviceToken = "ccdd"
	if err = s.Register(ctx, id, r, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = s.InvalidateToken(ctx, old, now.Add(2*time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Device(ctx, id)
	if !d.PushActive || d.Token != "ccdd" {
		t.Fatal("old response disabled new token")
	}
	if err = s.InvalidateToken(ctx, d, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Device(ctx, id)
	if !d.PushActive {
		t.Fatal("old invalidation timestamp")
	}
	if err = s.InvalidateToken(ctx, d, now.Add(2*time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Device(ctx, id)
	if d.PushActive {
		t.Fatal("token still active")
	}
	if _, err = s.Authenticate(ctx, token); err != nil {
		t.Fatal("invalid APNs token should not revoke API access")
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Authenticate(ctx, token); err != nil {
		t.Fatal("credential not persistent")
	}
	if err = s.Revoke(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revocation failed")
	}
}
func TestPairingFailureBudgetExpiryAndOwnership(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Now()
	secret := strings.Repeat("s", 32)
	code, _ := s.CreatePairing(ctx, now)
	bad := "00000000"
	if code == bad {
		bad = "99999999"
	}
	for range 5 {
		if _, err = s.Pair(ctx, bad, "phone", secret, now); !errors.Is(err, ErrUnauthorized) {
			t.Fatal(err)
		}
	}
	if _, err = s.Pair(ctx, code, "phone", secret, now); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("bruteforce budget not persisted")
	}
	code, _ = s.CreatePairing(ctx, now)
	if _, err = s.Pair(ctx, code, "phone", secret, now.Add(11*time.Minute)); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired pair")
	}
	code, _ = s.CreatePairing(ctx, now)
	_, err = s.Pair(ctx, code, "phone", secret, now)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Register(ctx, "phone", model.Registration{InstallationID: "victim", DeviceToken: "aabb", Environment: "production"}, now)
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("cross-device registration")
	}
}
