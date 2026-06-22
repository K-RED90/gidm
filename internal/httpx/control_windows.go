//go:build windows

package httpx

import "syscall"

// tcpBufControl returns a net.Dialer Control hook that sets SO_RCVBUF/SO_SNDBUF
// on the raw socket before connect. See the unix build for the rationale; on
// Windows the socket descriptor is a syscall.Handle. Best-effort: a failed
// setsockopt is ignored rather than failing the dial.
func tcpBufControl(recv, send int) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		return c.Control(func(fd uintptr) {
			if recv > 0 {
				_ = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, recv)
			}
			if send > 0 {
				_ = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, send)
			}
		})
	}
}
