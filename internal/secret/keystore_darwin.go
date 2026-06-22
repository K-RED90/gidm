//go:build darwin

package secret

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
)

// keyAccount is the generic-password account name our key is stored under.
const keyAccount = "credentials-key"

// errSecItemNotFound is the `security` exit code for a missing item.
const errSecItemNotFound = 44

// osKeyStore on macOS stores the master key in the login Keychain via the
// `security` CLI (the same os/exec approach the desktop uses to open files), so
// no cgo or third-party dependency is needed. keyPath is unused here.
func osKeyStore(service, _ string) keyStore {
	return &keychainStore{service: service}
}

type keychainStore struct {
	service string
}

func (s *keychainStore) get() ([]byte, error) {
	// -w prints just the password (our hex-encoded key) to stdout.
	out, err := exec.Command("security", "find-generic-password",
		"-s", s.service, "-a", keyAccount, "-w").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if ee.ExitCode() == errSecItemNotFound {
				return nil, errNoKey
			}
			return nil, errUnavailable
		}
		// `security` missing entirely (unheard of on macOS) → fall back to file.
		return nil, errUnavailable
	}
	key, err := hex.DecodeString(string(bytes.TrimSpace(out)))
	if err != nil {
		return nil, fmt.Errorf("secret: decode keychain key: %w", err)
	}
	return key, nil
}

func (s *keychainStore) set(key []byte) error {
	// -U updates an existing item instead of failing with errSecDuplicateItem.
	cmd := exec.Command("security", "add-generic-password",
		"-U", "-s", s.service, "-a", keyAccount, "-w", hex.EncodeToString(key))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("secret: store key in keychain: %w", err)
	}
	return nil
}
