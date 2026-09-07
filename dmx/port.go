package dmx

// Marker describes a serial line event that terminated a chunk of received data.
//
// DMX512 frames are delimited by a BREAK, not by anything in the data itself, so
// the receiver needs to know where breaks fall *relative to* the bytes around
// them. Reporting the event alongside the data it followed — rather than through
// a separate channel — is what makes that ordering guaranteed instead of racy.
type Marker uint8

const (
	// MarkerNone means the chunk ended because the buffer filled or the read
	// timed out, not because of a line event.
	MarkerNone Marker = iota
	// MarkerBreak is a BREAK condition: the start of the next DMX frame.
	MarkerBreak
	// MarkerError is a framing/parity error or overrun. The frame in progress is
	// unreliable and should be discarded, then resynced on the next break.
	MarkerError
)

func (m Marker) String() string {
	switch m {
	case MarkerBreak:
		return "break"
	case MarkerError:
		return "error"
	default:
		return "none"
	}
}

// Port is the platform-independent view of a DMX serial port.
type Port interface {
	// ReadChunk fills buf with the data bytes that arrived before the next line
	// event and reports that event.
	//
	// It blocks for at most ~100ms and returns (0, MarkerNone, nil) on an idle
	// timeout — never io.EOF for a timeout, and never a leaked EAGAIN, both of
	// which would spin the receiver loop.
	ReadChunk(buf []byte) (n int, m Marker, err error)

	Close() error
}

// Every platform's SerialPort must satisfy Port. This is deliberately in a file
// with no build tag, so a signature drift in any one implementation is a compile
// error on that platform rather than a surprise at the call site in main.
var _ Port = (*SerialPort)(nil)
