//go:build linux

package dmx

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// applyTermios sets the line discipline and the non-standard 250000 baud rate.
//
// Linux expresses a custom rate through BOTHER plus the termios2 ioctls rather
// than macOS's IOSSIOSPEED. No local struct is needed for that: unix.Termios on
// Linux is already byte-identical to struct termios2 — four uint32 flags, Line,
// Cc[19], Ispeed, Ospeed comes to 44 bytes, exactly the size encoded in
// TCSETS2 (0x402c542b).
func applyTermios(fd int, t *unix.Termios, baud int) error {
	t.Cflag = (t.Cflag &^ unix.CBAUD) | unix.BOTHER
	t.Ispeed = uint32(baud)
	t.Ospeed = uint32(baud)

	if err := unix.IoctlSetTermios(fd, unix.TCSETS2, t); err != nil {
		return fmt.Errorf("TCSETS2: %w", err)
	}
	return nil
}

func flushInput(fd int) error {
	return unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH)
}

// portGlobs are where USB serial adapters show up on Linux.
var portGlobs = []string{
	"/dev/ttyUSB*",
	"/dev/ttyACM*",
}

// PortExample is a representative port name for help text.
func PortExample() string { return "/dev/ttyUSB0" }

// PortHint tells the user how to find their adapter.
func PortHint() string {
	return "run `ls /dev/ttyUSB* /dev/ttyACM*`; you may need to be in the dialout group"
}
