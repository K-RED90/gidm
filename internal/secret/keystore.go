package secret

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// errNoKey means a store is reachable but holds no key yet (a first run).
// errUnavailable means the backing store cannot be used on this system at all
// (no secret-tool, no D-Bus session); either makes the caller fall through to
// the next store.
var (
	errNoKey       = errors.New("secret: no key in store")
	errUnavailable = errors.New("secret: key store unavailable")
)

// keyStore persists the 32-byte master key in some backing secret store.
type keyStore interface {
	get() ([]byte, error)
	set(key []byte) error
}

// keyStores is the ordered preference list: the OS secret store first (when
// usable on this platform), then the 0600 key file. The file store is always
// last so encryption keeps working on a headless host with no keyring.
func keyStores(service, keyPath string) []keyStore {
	stores := make([]keyStore, 0, 2)
	if ks := osKeyStore(service, keyPath); ks != nil {
		stores = append(stores, ks)
	}
	stores = append(stores, &fileStore{path: keyPath})
	return stores
}

// loadOrCreateKey returns the first key found across stores in order; if none
// holds one, it generates a key and writes it to the first store that accepts
// it (so the OS keychain wins over the file when both are usable). Assumes a
// single writer — gidmd runs single-instance — so there is no create race to
// arbitrate between two daemons.
func loadOrCreateKey(stores []keyStore) ([]byte, error) {
	for _, ks := range stores {
		key, err := ks.get()
		switch {
		case err == nil:
			if len(key) != keyLen {
				return nil, fmt.Errorf("secret: stored key is %d bytes, want %d", len(key), keyLen)
			}
			return key, nil
		case errors.Is(err, errNoKey), errors.Is(err, errUnavailable):
			continue
		default:
			return nil, err
		}
	}

	key, err := newKey()
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ks := range stores {
		if err := ks.set(key); err != nil {
			lastErr = err
			continue
		}
		return key, nil
	}
	if lastErr == nil {
		lastErr = errUnavailable
	}
	return nil, fmt.Errorf("secret: no usable key store: %w", lastErr)
}

// fileStore keeps the key as raw bytes in a 0600 file, its parent dir 0700. It
// is the universal fallback, reachable on every platform.
type fileStore struct {
	path string
}

func (s *fileStore) get() ([]byte, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNoKey
	}
	if err != nil {
		return nil, fmt.Errorf("secret: read key file %q: %w", s.path, err)
	}
	return b, nil
}

func (s *fileStore) set(key []byte) error {
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("secret: create key dir %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(s.path, key, 0o600); err != nil {
		return fmt.Errorf("secret: write key file %q: %w", s.path, err)
	}
	return nil
}
