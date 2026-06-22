package secret

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	v := newTestVault(t)
	cases := [][]byte{nil, {}, []byte("p@ss w0rd"), bytes.Repeat([]byte("x"), 4096)}
	for _, pt := range cases {
		ct, err := v.Seal(pt)
		if err != nil {
			t.Fatalf("seal %q: %v", pt, err)
		}
		got, err := v.Open(ct)
		if err != nil {
			t.Fatalf("open %q: %v", pt, err)
		}
		if !bytes.Equal(got, pt) {
			t.Fatalf("round-trip mismatch: got %q want %q", got, pt)
		}
	}
}

func TestSealIsNondeterministic(t *testing.T) {
	v := newTestVault(t)
	a, _ := v.Seal([]byte("same"))
	b, _ := v.Seal([]byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext are identical; nonce is not random")
	}
}

func TestOpenRejectsTamper(t *testing.T) {
	v := newTestVault(t)
	ct, _ := v.Seal([]byte("secret"))
	ct[len(ct)-1] ^= 0xff // flip a tag byte
	if _, err := v.Open(ct); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("tampered open: got %v, want ErrCorrupt", err)
	}
}

func TestOpenRejectsShort(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Open([]byte{1, 2, 3}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("short open: got %v, want ErrCorrupt", err)
	}
}

func TestOpenRejectsForeignKey(t *testing.T) {
	dir := t.TempDir()
	v1, _ := NewFromFile(filepath.Join(dir, "k1"))
	v2, _ := NewFromFile(filepath.Join(dir, "k2"))
	ct, _ := v1.Seal([]byte("secret"))
	if _, err := v2.Open(ct); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("foreign-key open: got %v, want ErrCorrupt", err)
	}
}

func TestKeyFilePersistsAcrossVaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	v1, err := NewFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ct, _ := v1.Seal([]byte("durable"))

	v2, err := NewFromFile(path) // same key file → same key
	if err != nil {
		t.Fatal(err)
	}
	got, err := v2.Open(ct)
	if err != nil || string(got) != "durable" {
		t.Fatalf("reopened vault: got %q err %v", got, err)
	}
}

func TestKeyFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits not meaningful on windows")
	}
	path := filepath.Join(t.TempDir(), "key")
	if _, err := NewFromFile(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file perms = %o, want 600", perm)
	}
}

func newTestVault(t *testing.T) Vault {
	t.Helper()
	v, err := NewFromFile(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("new vault: %v", err)
	}
	return v
}
