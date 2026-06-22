package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// keyLen is the master key size: 32 bytes selects AES-256.
const keyLen = 32

// ErrCorrupt is returned by Open when a ciphertext is too short, was not produced
// by this vault, or fails its GCM authentication tag (tampered, or sealed under a
// different key). Callers treat the secret as unreadable rather than trusting any
// decrypted bytes — a lost or rotated key degrades a download to "no credentials",
// it never crashes.
var ErrCorrupt = errors.New("secret: ciphertext is corrupt or was sealed with a different key")

// Vault seals and opens small secret blobs with authenticated encryption. A
// Vault is safe for concurrent use.
type Vault interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(ciphertext []byte) ([]byte, error)
}

// aesVault is an AES-256-GCM Vault. The sealed form is nonce || ciphertext; GCM
// authenticates both, so any modification (or the wrong key) fails Open.
type aesVault struct {
	gcm cipher.AEAD
}

func newAESVault(key []byte) (*aesVault, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("secret: key must be %d bytes, got %d", keyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secret: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret: new gcm: %w", err)
	}
	return &aesVault{gcm: gcm}, nil
}

// Seal encrypts plaintext under a fresh random nonce, returning nonce ||
// ciphertext. A nil/empty plaintext is sealed too (its length is hidden by GCM).
func (v *aesVault) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, v.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secret: nonce: %w", err)
	}
	// Seal appends the ciphertext onto nonce, so the result is exactly nonce || ct.
	return v.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Open reverses Seal. It returns ErrCorrupt for any input that is too short or
// fails authentication, so the caller can never act on forged plaintext.
func (v *aesVault) Open(ciphertext []byte) ([]byte, error) {
	ns := v.gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, ErrCorrupt
	}
	nonce, body := ciphertext[:ns], ciphertext[ns:]
	pt, err := v.gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, ErrCorrupt
	}
	return pt, nil
}

// New returns a Vault backed by AES-256-GCM. Its 32-byte master key is fetched
// from the OS secret store (macOS Keychain, Windows DPAPI, or the Linux Secret
// Service) under service, generated on first use. Where no OS store is reachable
// — a headless host, CI — the key falls back to a 0600 file at keyPath, so
// encryption still works without a desktop session.
func New(service, keyPath string) (Vault, error) {
	key, err := loadOrCreateKey(keyStores(service, keyPath))
	if err != nil {
		return nil, err
	}
	return newAESVault(key)
}

// NewFromFile returns a Vault whose key lives only in the 0600 file at keyPath,
// bypassing the OS keychain entirely. It is for environments where the keychain
// is unavailable or undesirable (tests, sandboxes).
func NewFromFile(keyPath string) (Vault, error) {
	key, err := loadOrCreateKey([]keyStore{&fileStore{path: keyPath}})
	if err != nil {
		return nil, err
	}
	return newAESVault(key)
}

// newKey returns 32 cryptographically random bytes.
func newKey() ([]byte, error) {
	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("secret: generate key: %w", err)
	}
	return key, nil
}
