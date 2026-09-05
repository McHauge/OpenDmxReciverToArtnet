//go:build windows

package artnet

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// setBroadcast enables SO_BROADCAST on a raw socket handle. Windows already
// permits broadcast sends without it; this is set anyway so every platform
// requests the same socket semantics.
func setBroadcast(c syscall.RawConn) error {
	var sockErr error
	if err := c.Control(func(fd uintptr) {
		sockErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return sockErr
}
