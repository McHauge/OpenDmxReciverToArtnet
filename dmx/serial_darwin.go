//go:build darwin

package dmx

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	// iossIOSpeed is IOSSIOSPEED = _IOW('T', 2, speed_t) from
	// <IOKit/serial/ioss.h>. speed_t on macOS is unsigned long, so 8 bytes in a
	// 64-bit process, giving 0x80085402.
	//
	// The value copied around most often is 0x80045402 — that is IOSSIOSPEED_32,
	// which the header also defines and the kernel handles separately, which is
	// why it appears to work. Pairing it with a uint64 argument would leave the
	// top half of the rate unread, so use the 64-bit request here.
	iossIOSpeed = 0x80085402

	// iossDataLat is IOSSDATALAT = _IOW('T', 0, unsigned long): receive latency
	// in microseconds.
	iossDataLat = 0x80085400

	// fRead is FREAD from <sys/fcntl.h>, the argument TIOCFLUSH expects.
	fRead = 0x1
)

// applyTermios sets the line discipline and then the non-standard 250000 baud
// rate, which cfsetspeed cannot express on macOS.
func applyTermios(fd int, t *unix.Termios, baud int) error {
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, t); err != nil {
		return fmt.Errorf("TIOCSETA: %w", err)
	}

	// Order matters: tcsetattr resets the speed, so IOSSIOSPEED has to come
	// after TIOCSETA and nothing may call TIOCSETA again afterwards.
	//
	// unix.IoctlSetPointerInt is deliberately not used here: it passes a pointer
	// to an int32, and this request's size field says 8 bytes, so the kernel
	// would copy four bytes of adjacent stack into the high half of the rate.
	speed := uint64(baud)
	if _, _, e := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		uintptr(iossIOSpeed),
		uintptr(unsafe.Pointer(&speed)),
	); e != 0 {
		return fmt.Errorf("IOSSIOSPEED(%d): %w", baud, e)
	}

	// The driver's default receive latency is a 256/3-character delay, roughly
	// 3.8ms at 250kbaud, which shows up as frame-timing jitter. Best effort.
	lat := uint64(1000)
	_, _, _ = unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		uintptr(iossDataLat),
		uintptr(unsafe.Pointer(&lat)),
	)

	return nil
}

func flushInput(fd int) error {
	return unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, fRead)
}

// portGlobs are where USB serial adapters show up on macOS. The cu.* ("call-up")
// nodes are the right ones for a receiver: unlike tty.*, opening them does not
// block waiting for carrier detect.
var portGlobs = []string{
	"/dev/cu.usbserial*",
	"/dev/cu.usbmodem*",
	"/dev/cu.SLAB_USBtoUART*",
}

// PortExample is a representative port name for help text.
func PortExample() string { return "/dev/cu.usbserial-A1B2C3" }

// PortHint tells the user how to find their adapter.
func PortHint() string { return "run `ls /dev/cu.*` to list serial devices" }

// Apple's AppleUSBFTDI driver does not report BREAK to the tty layer, so PARMRK
// never escapes one into the read stream — measured against a DSD TECH SH-RS09B
// on macOS 15 with a live 44fps source: PARMRK/INPCK read back set and
// IGNBRK/BRKINT/IGNPAR clear, yet zero FF 00 00 sequences in two seconds where
// ~88 breaks should appear. Setting IGNBRK changes nothing, which shows the tty
// layer never sees a break at all.
//
// What does survive is the FTDI chip's own behaviour. The UART turns the break
// into a 0x00 data byte (the BI status flag that would label it is stripped by
// the driver), and because the line then idles through the break and
// mark-after-break, the chip flushes a partial USB packet. So a read shorter
// than the 62-byte payload marks the end of a frame, and the next byte is the
// start code.
//
// Measured: 76 of 76 short reads were followed by 0x00, with a consistent
// 514-byte spacing — 513 for the packet plus the break's stray byte, which the
// receiver discards when the channel count is already full.
const (
	BreakDetectSupported = true
	breakFromShortRead   = true

	// usbPayloadSize is an FTDI bulk-in packet (64 bytes) less its two status
	// bytes. A read below this means the adapter's buffer drained.
	usbPayloadSize = 62
)
