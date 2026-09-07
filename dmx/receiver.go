package dmx

import (
	"context"
	"fmt"
	"time"
)

// DefaultGapThreshold is the idle time after which fallback mode treats the
// line as having gone quiet between frames. See runFallback for why this is
// only a resync hint and not a primary framing mechanism.
const DefaultGapThreshold = 2 * time.Millisecond

// PacketSize is a full DMX packet on the wire: the start code plus 512 channels.
const PacketSize = 1 + MaxChannels

// findAlignment locates the start-code offset in an unframed DMX byte stream.
//
// Packets are a fixed PacketSize apart, so the start code recurs at that stride.
// The correct offset is the one where every boundary visible in buf holds the
// zero start code. Latching on the first zero byte instead would almost always
// pick a channel value, since most DMX channels sit at zero.
//
// With an all-zero universe every offset qualifies and the first is returned;
// that is harmless, because every alignment yields identical output until real
// data appears — at which point the start-code check in runFallback rejects a
// wrong alignment and hunts again.
func findAlignment(buf []byte) (int, bool) {
	if len(buf) < 2*PacketSize {
		return 0, false
	}
	for off := 0; off < PacketSize; off++ {
		aligned := true
		for p := off; p+PacketSize < len(buf); p += PacketSize {
			if buf[p] != StartCodeDMX || buf[p+PacketSize] != StartCodeDMX {
				aligned = false
				break
			}
		}
		if aligned {
			return off, true
		}
	}
	return 0, false
}

type Receiver struct {
	port          Port
	Frames        chan Frame
	noBreakDetect bool

	// GapThreshold tunes fallback mode only. Set it before calling Run.
	GapThreshold time.Duration

	// inferBreaks records that this platform derives BREAK events from read
	// sizes rather than a driver signal, so they need validating before use.
	// It mirrors breakFromShortRead; tests override it to cover both paths.
	inferBreaks bool
}

func NewReceiver(port Port, noBreakDetect bool) *Receiver {
	return &Receiver{
		port:          port,
		Frames:        make(chan Frame, 4),
		noBreakDetect: noBreakDetect,
		GapThreshold:  DefaultGapThreshold,
		inferBreaks:   breakFromShortRead,
	}
}

func (r *Receiver) Run(ctx context.Context) error {
	if r.noBreakDetect {
		return r.runFallback(ctx)
	}
	return r.runWithBreakDetect(ctx)
}

// breakVoteWindow is how many BREAK events are sampled before locking onto the
// source's packet length.
const breakVoteWindow = 24

// breakValidator rejects BREAK events that cannot be real frame boundaries.
//
// Where the driver reports BREAK directly (Windows, Linux) every event is
// genuine and this passes them all through. macOS has no such signal and infers
// breaks from short reads, which is noisy: the tty layer hands data over
// incrementally, so a read often ends mid-packet for reasons having nothing to
// do with the line going idle. Measured on live hardware, 18 of 114 inferred
// breaks were spurious, arriving at frame lengths of 1, 5, 17, 327, 366...
//
// The real ones all agree on the source's packet length, so the fix is to learn
// that length by majority vote and then require it. A break at any other length
// is dropped and the frame keeps accumulating, which is exactly right: the data
// itself was never wrong, only the boundary claim.
type breakValidator struct {
	votes  map[int]int
	locked int
	seen   int
	infer  bool // breaks are inferred from read sizes, so they need vetting
}

func newBreakValidator(infer bool) *breakValidator {
	return &breakValidator{votes: make(map[int]int), infer: infer}
}

// classify reports whether a BREAK at this frame length ends the frame, and
// whether that frame is trustworthy enough to publish.
//
// The two answers differ only while learning: every break is treated as a
// boundary so the vote sees true inter-break distances, but nothing is
// published, because at that point real and spurious breaks are
// indistinguishable and publishing the spurious ones puts visible garbage on
// the output for the fraction of a second before the lock takes.
func (v *breakValidator) classify(length int) (boundary, publish bool) {
	if !v.infer {
		return true, true // the driver told us; no second-guessing needed
	}

	if v.locked > 0 {
		if length == v.locked {
			return true, true
		}
		// Persistently wrong means the source changed its packet size, so
		// relearn rather than stall forever.
		v.votes[length]++
		if v.votes[length] >= breakVoteWindow {
			v.locked = 0
			v.seen = 0
			v.votes = make(map[int]int)
		}
		return false, false
	}

	if length > 0 {
		v.votes[length]++
		v.seen++
		if v.seen >= breakVoteWindow {
			best, bestCount := 0, 0
			for l, c := range v.votes {
				if c > bestCount || (c == bestCount && l > best) {
					best, bestCount = l, c
				}
			}
			v.locked = best
			v.votes = make(map[int]int)
		}
	}
	return true, false
}

// emit publishes a completed frame, dropping it if the consumer is behind.
// Dropping is correct here: DMX is a continuous state broadcast, so a stale
// frame is worth less than the next fresh one.
func (r *Receiver) emit(frame Frame) {
	frame.Timestamp = time.Now()
	select {
	case r.Frames <- frame:
	default:
	}
}

// runWithBreakDetect frames on the BREAK that delimits every DMX packet.
//
// ReadChunk hands back the data that arrived before a line event together with
// the event itself, so the break is always applied at the exact byte offset it
// occurred at. That ordering is the whole point: a break reported even slightly
// out of position truncates one frame and desyncs the next.
func (r *Receiver) runWithBreakDetect(ctx context.Context) error {
	buf := make([]byte, 1024)
	var frame Frame
	state := stateWaitBreak
	breaks := newBreakValidator(r.inferBreaks)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		n, marker, err := r.port.ReadChunk(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Surface it rather than looping. A persistent error here means the
			// adapter is gone; retrying in a tight loop would just burn a core.
			return fmt.Errorf("read: %w", err)
		}

		for i := 0; i < n; i++ {
			switch state {
			case stateWaitStartCode:
				frame.StartCode = buf[i]
				if buf[i] == StartCodeDMX {
					state = stateReadData
				} else {
					// Alternate start code (RDM, text packet, ...) — not ours.
					state = stateWaitBreak
				}

			case stateReadData:
				if frame.Length < MaxChannels {
					frame.Channels[frame.Length] = buf[i]
					frame.Length++
				}
				// Once the source's packet length is known, close the frame on
				// length rather than waiting for a break. Inferred breaks are
				// missed whenever the boundary read happens to be a full-size
				// one, which costs about a quarter of the frame rate; the
				// length is exact every time.
				if r.inferBreaks && breaks.locked > 0 && frame.Length == breaks.locked {
					r.emit(frame)
					frame = Frame{}
					state = stateSkipTrailer
				}

			case stateSkipTrailer:
				// The break's stray byte. Discard it; the next byte is the
				// start code.
				state = stateWaitStartCode

				// stateWaitBreak: discard until the next break resyncs us.
			}
		}

		switch marker {
		case MarkerBreak:
			if r.inferBreaks && breaks.locked > 0 {
				// Length drives framing now. Breaks are only useful to recover
				// phase after a desync, and acting on them otherwise would
				// clobber the trailer-skip state.
				if state == stateWaitBreak {
					state = stateWaitStartCode
				}
				continue
			}
			if state == stateReadData && frame.Length > 0 {
				boundary, publish := breaks.classify(frame.Length)
				if !boundary {
					// Not a real boundary. Keep the frame open — the bytes so
					// far are still valid, only the claim about where the
					// packet ends was wrong.
					continue
				}
				if publish {
					r.emit(frame)
				}
			}
			frame = Frame{}
			state = stateWaitStartCode

		case MarkerError:
			// Framing/parity error or overrun: the frame in flight is suspect.
			frame = Frame{}
			state = stateWaitBreak
		}
	}
}

// runFallback frames without trusting BREAK detection.
//
// It anchors on length: DMX packets carry a start code plus up to 512 channel
// bytes, so after 512 channels the next byte is the following packet's start
// code. That holds sync indefinitely against any source sending full-size
// frames, and it is the only mechanism here that works on real hardware.
//
// The idle gap is a resync hint only, never the primary boundary. At 44fps the
// mark-before-break is about 96us, while an FTDI adapter's latency timer batches
// USB transfers on a millisecond scale — the wire timing simply does not survive
// to userspace, so a gap threshold cannot resolve the break. Sources that send
// short frames still need it to find the boundary at all, which is why it stays.
func (r *Receiver) runFallback(ctx context.Context) error {
	buf := make([]byte, 1024)
	var frame Frame
	var syncBuf []byte // accumulated while hunting for the packet boundary
	state := stateWaitBreak
	lastData := time.Now()

	gap := r.GapThreshold
	if gap <= 0 {
		gap = DefaultGapThreshold
	}

	fmt.Println("Running in fallback mode (no BREAK detection)")
	if !BreakDetectSupported {
		fmt.Println("This platform's serial driver does not report BREAK, so this is the default here.")
	}
	fmt.Printf("Framing on packet length, resyncing on gaps over %v...\n", gap)

	// consume feeds aligned bytes through the length-anchored state machine.
	consume := func(data []byte) {
		for _, b := range data {
			switch state {
			case stateWaitStartCode:
				frame.StartCode = b
				if b == StartCodeDMX {
					state = stateReadData
				} else {
					// Alignment was wrong, or the source sent an alternate
					// start code. Either way, hunt for the boundary again.
					state = stateWaitBreak
				}

			case stateReadData:
				frame.Channels[frame.Length] = b
				frame.Length++
				if frame.Length == MaxChannels {
					r.emit(frame)
					frame = Frame{}
					state = stateWaitStartCode
				}
			}
		}
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		n, marker, err := r.port.ReadChunk(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read: %w", err)
		}

		now := time.Now()

		if n == 0 {
			if now.Sub(lastData) < gap {
				continue
			}
			switch {
			case state == stateReadData && frame.Length > 0:
				// A partial frame was open and the line went quiet: close it out.
				r.emit(frame)
				frame = Frame{}
				state = stateWaitStartCode

			case state == stateWaitBreak && len(syncBuf) > 1:
				// Still hunting, but the line went quiet. A source sending
				// short packets never accumulates the two full packets
				// findAlignment needs, so the gap is its only boundary signal:
				// take the buffered bytes as one complete packet.
				if syncBuf[0] == StartCodeDMX {
					var f Frame
					f.StartCode = StartCodeDMX
					f.Length = copy(f.Channels[:], syncBuf[1:])
					r.emit(f)
				}
				syncBuf = syncBuf[:0]
				state = stateWaitStartCode
			}
			continue
		}
		lastData = now

		// A hardware error still means resync, even here.
		if marker == MarkerError {
			frame = Frame{}
			state = stateWaitBreak
		}

		chunk := buf[:n]

		// Unsynced: buffer until the packet boundary can be located, rather
		// than guessing from a single zero byte.
		if state == stateWaitBreak {
			syncBuf = append(syncBuf, chunk...)
			off, ok := findAlignment(syncBuf)
			if !ok {
				// Keep the hunting window bounded.
				if len(syncBuf) > 4*PacketSize {
					syncBuf = append(syncBuf[:0], syncBuf[len(syncBuf)-2*PacketSize:]...)
				}
				continue
			}
			state = stateWaitStartCode
			chunk = append([]byte(nil), syncBuf[off:]...)
			syncBuf = syncBuf[:0]
		}

		consume(chunk)
	}
}
