//go:build !darwin && !linux && !windows

package secret

// osKeyStore has no OS secret store wired up on this platform, so encryption
// always uses the 0600 key-file fallback.
func osKeyStore(_, _ string) keyStore { return nil }
