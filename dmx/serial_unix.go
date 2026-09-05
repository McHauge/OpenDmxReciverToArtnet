//go:build darwin || linux

package dmx

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// readTimeout mirrors the Windows ReadTotalTimeoutConstant: a read that finds
// nothing gives up after this long and reports an idle line.
const readTimeout = 100 * time.Millisecond

// rawBufSize bounds one read. A DMX packet is 513 bytes plus PARMRK escaping,
// so this comfortably holds several frames' worth of a USB batch.
const rawBufSize = 4096

// SerialPort receives DMX512 from a serial adapter on darwin and linux.
//
// The fd is opened non-blocking and handed to os.File, which registers it with
// the runtime poller (kqueue/epoll). That buys three things for free:
// SetReadDeadline for the read timeout, EAGAIN handled internally so it can
// never leak to the caller, and refcounted Close that safely unblocks a read in
// flight on another goroutine — no self-pipe, no closed flag, no join.
type SerialPort struct {
	f        *os.File
	name     string
	pollable bool

	dec          parmrkDecoder
	carry        []byte // read but not yet decoded; empty whenever we read again
	out          []byte // decoded, not yet returned to the caller
	mark         Marker // event pending once out drains
	pendingBreak bool   // a short read is waiting to be reported as a break

	carryBuf [rawBufSize]byte
	outBuf   [rawBufSize]byte
}

func OpenSerialPort(portName string) (*SerialPort, error) {
	path := resolvePortPath(portName)

	// O_RDWR rather than O_RDONLY: we never write, but some drivers reject
	// modem-line ioctls on a read-only fd. O_NONBLOCK also stops open(2)
	// blocking on carrier detect.
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", path, err)
	}

	if err := configurePort(fd); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("configure %s: %w", path, err)
	}

	sp := &SerialPort{f: os.NewFile(uintptr(fd), path), name: path}
	if sp.f == nil {
		unix.Close(fd)
		return nil, fmt.Errorf("cannot wrap %s", path)
	}

	sp.pollable = sp.f.SetReadDeadline(time.Time{}) == nil
	if !sp.pollable {
		// No deadline support, so the timeout has to come from VTIME instead.
		if err := blockingTermios(fd); err != nil {
			sp.f.Close()
			return nil, fmt.Errorf("configure %s for blocking reads: %w", path, err)
		}
	}

	return sp, nil
}

// baseTermios builds the line settings from zero rather than editing what the
// driver happens to have. The defaults are actively hostile here: ICRNL would
// rewrite 0x0D channel values to 0x0A, ICANON would buffer by line, and BRKINT
// would raise SIGINT 44 times a second.
func baseTermios() unix.Termios {
	var t unix.Termios

	// PARMRK+INPCK escape BREAK and framing errors into the read stream as
	// FF 00 00 / FF 00 XX. IGNBRK, BRKINT and IGNPAR must stay clear or the
	// events are swallowed before we see them. See parmrk.go.
	t.Iflag = unix.PARMRK | unix.INPCK
	t.Oflag = 0
	// DMX512 line settings: 8 data bits, no parity, 2 stop bits.
	t.Cflag = unix.CS8 | unix.CSTOPB | unix.CREAD | unix.CLOCAL
	t.Lflag = 0

	// VMIN=1 means a read never completes with zero bytes. That matters more
	// than it looks: os.File is built with ZeroReadIsEOF, so under VMIN=0 an
	// idle line returns 0 bytes and surfaces as a spurious io.EOF on every
	// read after the first. With VMIN=1 an idle fd yields EAGAIN, which the
	// runtime poller absorbs, the read deadline supplies the timeout, and a
	// zero-byte read once again means a genuine hangup.
	//
	// VTIME is unused here (tenths of a second cannot express anything useful
	// at this data rate); the deadline in ReadChunk bounds the wait instead.
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0

	// A placeholder standard rate, replaced by applyTermios with the real
	// 250000. It has to be a legal rate: leaving these zero means B0, which
	// asks the driver to hang up the line, and an FTDI adapter rejects that
	// outright with EINVAL rather than ignoring it.
	t.Ispeed, t.Ospeed = 9600, 9600

	return t
}

// blockingTermios adapts the line settings for the case where the fd did not
// make it into the runtime poller. Without the poller there is no read
// deadline, so VTIME has to provide the timeout itself and VMIN must be 0 for
// it to apply — which reintroduces zero-byte reads, handled in ReadChunk.
func blockingTermios(fd int) error {
	t := baseTermios()
	t.Cc[unix.VMIN] = 0
	t.Cc[unix.VTIME] = 1 // 100ms, matching readTimeout
	return applyTermios(fd, &t, dmxBaudRate)
}

func configurePort(fd int) error {
	// Exclusive access, matching CreateFile's dwShareMode = 0 on Windows.
	if err := unix.IoctlSetInt(fd, unix.TIOCEXCL, 0); err != nil {
		return fmt.Errorf("TIOCEXCL: %w", err)
	}

	t := baseTermios()
	if err := applyTermios(fd, &t, dmxBaudRate); err != nil {
		return err
	}

	// Drop DTR and RTS, matching the Windows DCB flags (fDtrControl and
	// fRtsControl both DISABLE). Must come after tcsetattr, which can reassert
	// the lines. Best effort: not every adapter exposes modem control.
	_ = unix.IoctlSetPointerInt(fd, unix.TIOCMBIC, unix.TIOCM_DTR|unix.TIOCM_RTS)

	// Discard anything buffered before we started listening, so we do not
	// decode the tail of a frame that began before open.
	_ = flushInput(fd)

	return nil
}

// ReadChunk implements Port.
func (sp *SerialPort) ReadChunk(buf []byte) (int, Marker, error) {
	for {
		// Serve whatever the decoder already produced.
		if len(sp.out) > 0 {
			n := copy(buf, sp.out)
			sp.out = sp.out[n:]
			if len(sp.out) > 0 {
				// Caller's buffer filled first; the event stays pending.
				return n, MarkerNone, nil
			}
			m := sp.mark
			sp.mark = MarkerNone
			return n, m, nil
		}
		if sp.mark != MarkerNone {
			m := sp.mark
			sp.mark = MarkerNone
			return 0, m, nil
		}

		// Decode what is left over from the last read. The decoder stops at the
		// first event, so this can take several passes.
		if len(sp.carry) > 0 {
			out, m, consumed := sp.dec.decode(sp.outBuf[:0], sp.carry)
			sp.carry = sp.carry[consumed:]
			if m == MarkerNone && len(sp.carry) == 0 && sp.pendingBreak {
				m = MarkerBreak
				sp.pendingBreak = false
			}
			sp.out, sp.mark = out, m
			continue
		}

		if sp.pollable {
			if err := sp.f.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
				return 0, MarkerNone, err
			}
		}

		n, err := sp.f.Read(sp.carryBuf[:])
		switch {
		case errors.Is(err, os.ErrDeadlineExceeded):
			// Idle line. Never surface this as an error: the receiver would
			// treat it as a dead adapter.
			return 0, MarkerNone, nil
		case errors.Is(err, io.EOF) && !sp.pollable:
			// Blocking path only: os.File maps a 0-byte read to EOF, which here
			// just means VTIME expired.
			return 0, MarkerNone, nil
		case err != nil:
			// Includes a genuine EOF on the pollable path, i.e. hangup.
			return 0, MarkerNone, err
		}
		sp.carry = sp.carryBuf[:n]

		// A read shorter than the adapter's USB payload means its buffer
		// drained, which on a continuously transmitting DMX line only happens
		// during the BREAK. Where the driver reports no break of its own, that
		// flush is the break signal. See breakFromShortRead.
		if breakFromShortRead && n < usbPayloadSize {
			sp.pendingBreak = true
		}
	}
}

func (sp *SerialPort) Close() error {
	// os.File refcounts: this unblocks a concurrent Read and defers the real
	// close(2) until that read returns, so the fd can never be reused underneath.
	return sp.f.Close()
}
