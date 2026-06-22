//go:build windows

package secret

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// osKeyStore on Windows protects the master key with DPAPI (CryptProtectData,
// current-user scope) and stores the protected blob in a file beside the
// fallback key path. DPAPI ties decryption to the logged-in account, so the
// on-disk blob is useless to another user. No cgo, no third-party dependency:
// crypt32.dll is called directly via syscall. service is unused here.
func osKeyStore(_, keyPath string) keyStore {
	return &dpapiStore{path: keyPath + ".dpapi"}
}

type dpapiStore struct {
	path string
}

func (s *dpapiStore) get() ([]byte, error) {
	blob, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNoKey
	}
	if err != nil {
		return nil, fmt.Errorf("secret: read dpapi blob: %w", err)
	}
	key, err := dpapiUnprotect(blob)
	if err != nil {
		return nil, fmt.Errorf("secret: dpapi unprotect: %w", err)
	}
	return key, nil
}

func (s *dpapiStore) set(key []byte) error {
	blob, err := dpapiProtect(key)
	if err != nil {
		return fmt.Errorf("secret: dpapi protect: %w", err)
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("secret: create key dir: %w", err)
		}
	}
	if err := os.WriteFile(s.path, blob, 0o600); err != nil {
		return fmt.Errorf("secret: write dpapi blob: %w", err)
	}
	return nil
}

var (
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtect   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
	procLocalFree      = kernel32.NewProc("LocalFree")
)

// dataBlob is DATA_BLOB: a length-prefixed byte buffer DPAPI reads and writes.
type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(d []byte) dataBlob {
	if len(d) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(d)), pbData: &d[0]}
}

func (b dataBlob) bytes() []byte {
	out := make([]byte, b.cbData)
	if b.cbData > 0 {
		copy(out, unsafe.Slice(b.pbData, b.cbData))
	}
	return out
}

// cryptprotectUIForbidden bars any interactive prompt, so a headless service
// never blocks on a UI dialog.
const cryptprotectUIForbidden = 0x1

func dpapiProtect(data []byte) ([]byte, error) {
	in := newBlob(data)
	var out dataBlob
	r, _, err := procCryptProtect.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0,
		cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

func dpapiUnprotect(data []byte) ([]byte, error) {
	in := newBlob(data)
	var out dataBlob
	r, _, err := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0,
		cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}
