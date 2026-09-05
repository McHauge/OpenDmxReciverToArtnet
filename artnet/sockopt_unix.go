//go:build !windows

package artnet

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// setBroadcast enables SO_BROADCAST on a raw socket handle.
//
// Go does not set this itself. Linux rejects a sendto to a broadcast address
// without it (EACCES), which matters because 255.255.255.255 is the default
// Art-Net destination. Measured on macOS 15, Darwin permits the send either
// way, for both the limited and subnet-directed forms — so this is a real fix
// for Linux and belt-and-braces elsewhere.
func setBroadcast(c syscall.RawConn) error {
	var sockErr error
	if err := c.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return sockErr
}
