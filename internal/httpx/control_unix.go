//go:build !windows

package httpx

import "syscall"

// tcpBufControl returns a net.Dialer Control hook that sets SO_RCVBUF/SO_SNDBUF
// on the raw socket before connect, sizing the kernel's TCP buffers. A large
// receive buffer lets a single connection fill a high bandwidth-delay-product
// link. Non-positive sizes are skipped (leaving the OS default). Sizing is
// best-effort tuning: a setsockopt failure is ignored, never failing the dial.
func tcpBufControl(recv, send int) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		return c.Control(func(fd uintptr) {
			if recv > 0 {
				_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, recv)
			}
			if send > 0 {
				_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, send)
			}
		})
	}
}
