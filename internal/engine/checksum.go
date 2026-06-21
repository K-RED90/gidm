package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// checksumPrefix is the only algorithm gidm verifies today. The Download.Checksum
// field carries values like "sha256:<hex>".
const checksumPrefix = "sha256:"

// parseChecksum splits a "sha256:<hex>" value into its algorithm and lowercased
// hex digest. ok is false when s is empty or not a recognized form, so an absent
// checksum simply skips verification.
func parseChecksum(s string) (algo, hexSum string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	if rest, found := strings.CutPrefix(s, checksumPrefix); found {
		return "sha256", strings.ToLower(strings.TrimSpace(rest)), true
	}
	return "", "", false
}

// verifyChecksum hashes the file at path with SHA-256 in a single sequential
// pass and compares it to wantHex. It never loads the file into memory; the
// buffer here is a cold-path allocation (finalize, off the per-chunk hot path).
// ctx cancellation aborts the read.
func verifyChecksum(ctx context.Context, path, wantHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("engine: verify checksum: open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	buf := make([]byte, defaultBufferSize)
	if _, err := io.CopyBuffer(h, &ctxReader{ctx: ctx, r: f}, buf); err != nil {
		return fmt.Errorf("engine: verify checksum: read %q: %w", path, err)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		return fmt.Errorf("engine: checksum mismatch: got sha256:%s, want sha256:%s", got, wantHex)
	}
	return nil
}

// ctxReader makes a blocking Read honor ctx cancellation between chunks. The
// underlying read of one buffer still runs to completion, which is fine for a
// local file.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (cr *ctxReader) Read(p []byte) (int, error) {
	if err := cr.ctx.Err(); err != nil {
		return 0, err
	}
	return cr.r.Read(p)
}
