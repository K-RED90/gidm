package sqlite

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/K-RED90/gidm/internal/engine"
	"github.com/K-RED90/gidm/internal/secret"
)

func newVaultStore(t *testing.T, dir string) *Store {
	t.Helper()
	v, err := secret.NewFromFile(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	s, err := New(filepath.Join(dir, "gidm.db"), WithVault(v))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCredentialsRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newVaultStore(t, t.TempDir())

	d := sampleDownload("creds")
	d.Auth = &engine.RequestOptions{
		Username: "user",
		Password: "s3cr3t-pa$$",
		Referer:  "https://ref.example/",
		Cookie:   "sid=abc123",
		Headers:  map[string]string{"X-Token": "bearer.xyz"},
	}
	if err := s.SaveDownload(ctx, d); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}
	got, err := s.LoadDownload(ctx, d.ID)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if got.Auth == nil {
		t.Fatal("Auth not loaded")
	}
	if got.Auth.Username != "user" || got.Auth.Password != "s3cr3t-pa$$" ||
		got.Auth.Referer != "https://ref.example/" || got.Auth.Cookie != "sid=abc123" ||
		got.Auth.Headers["X-Token"] != "bearer.xyz" {
		t.Fatalf("Auth mismatch: %+v", got.Auth)
	}
}

func TestCredentialsStoredEncrypted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newVaultStore(t, t.TempDir())

	d := sampleDownload("enc")
	d.Auth = &engine.RequestOptions{Password: "PLAINTEXT-NEEDLE"}
	if err := s.SaveDownload(ctx, d); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}
	var blob []byte
	if err := s.db.QueryRowContext(ctx, "SELECT credentials FROM downloads WHERE id = ?", d.ID).Scan(&blob); err != nil {
		t.Fatalf("read raw credentials: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("credentials column is empty")
	}
	if bytes.Contains(blob, []byte("PLAINTEXT-NEEDLE")) {
		t.Fatal("password stored in plaintext in the credentials column")
	}
}

func TestCredentialsClearedWhenNil(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newVaultStore(t, t.TempDir())

	d := sampleDownload("clear")
	d.Auth = &engine.RequestOptions{Username: "u", Password: "p"}
	if err := s.SaveDownload(ctx, d); err != nil {
		t.Fatalf("save with auth: %v", err)
	}
	d.Auth = nil
	if err := s.SaveDownload(ctx, d); err != nil {
		t.Fatalf("save clearing auth: %v", err)
	}
	got, err := s.LoadDownload(ctx, d.ID)
	if err != nil {
		t.Fatalf("LoadDownload: %v", err)
	}
	if got.Auth != nil {
		t.Fatalf("Auth should be nil after clear, got %+v", got.Auth)
	}
}

func TestCredentialsWithoutVaultRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t) // no vault wired

	d := sampleDownload("novault")
	d.Auth = &engine.RequestOptions{Password: "p"}
	if err := s.SaveDownload(ctx, d); err == nil {
		t.Fatal("expected an error saving credentials without a vault")
	}
	// A download without auth still saves fine on a vault-less store.
	if err := s.SaveDownload(ctx, sampleDownload("novault-ok")); err != nil {
		t.Fatalf("save without auth: %v", err)
	}
}

func TestCredentialsDegradeOnKeyLoss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gidm.db")

	v1, _ := secret.NewFromFile(filepath.Join(dir, "key1"))
	s1, err := New(dbPath, WithVault(v1))
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}
	d := sampleDownload("keyloss")
	d.Auth = &engine.RequestOptions{Username: "u", Password: "p"}
	if err := s1.SaveDownload(ctx, d); err != nil {
		t.Fatalf("save: %v", err)
	}
	_ = s1.Close()

	// Reopen with a different key (a lost or rotated key): the record must still
	// load, just without its now-undecryptable credentials.
	v2, _ := secret.NewFromFile(filepath.Join(dir, "key2"))
	s2, err := New(dbPath, WithVault(v2))
	if err != nil {
		t.Fatalf("New s2: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	got, err := s2.LoadDownload(ctx, d.ID)
	if err != nil {
		t.Fatalf("LoadDownload after key loss: %v", err)
	}
	if got.Auth != nil {
		t.Fatal("expected Auth to degrade to nil on key loss")
	}
}
