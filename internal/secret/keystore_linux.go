//go:build linux

package secret

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// keyAttr labels our key in the Secret Service schema.
const keyAttr = "credentials-key"

// osKeyStore on Linux uses libsecret's `secret-tool` to store the key in the
// Secret Service (GNOME Keyring / KWallet). When secret-tool is not installed —
// typical on a headless server — it reports nil so the caller uses the 0600 key
// file. keyPath is unused here.
func osKeyStore(service, _ string) keyStore {
	bin, err := exec.LookPath("secret-tool")
	if err != nil {
		return nil
	}
	return &secretToolStore{bin: bin, service: service}
}

type secretToolStore struct {
	bin     string
	service string
}

func (s *secretToolStore) get() ([]byte, error) {
	out, err := exec.Command(s.bin, "lookup", "service", s.service, "key", keyAttr).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// Exit non-zero (commonly 1) = not found or no session; treat as a
			// first run and let set() decide whether the backend is truly usable.
			return nil, errNoKey
		}
		return nil, errUnavailable
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, errNoKey
	}
	key, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("secret: decode secret-tool key: %w", err)
	}
	return key, nil
}

func (s *secretToolStore) set(key []byte) error {
	// secret-tool store reads the secret from stdin until EOF.
	cmd := exec.Command(s.bin, "store", "--label=gidm credentials key",
		"service", s.service, "key", keyAttr)
	cmd.Stdin = bytes.NewReader([]byte(hex.EncodeToString(key)))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("secret: store key via secret-tool: %w", err)
	}
	return nil
}
