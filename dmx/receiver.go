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

type Receiver struct {
	port          Port
	Frames        chan Frame
	noBreakDetect bool

	// GapThreshold tunes fallback mode only. Set it before calling Run.
	GapThreshold time.Duration
}

func NewReceiver(port Port, noBreakDetect bool) *Receiver {
	return &Receiver{
		port:          port,
		Frames:        make(chan Frame, 4),
		noBreakDetect: noBreakDetect,
		GapThreshold:  DefaultGapThreshold,
	}
}

func (r *Receiver) Run(ctx context.Context) error {
	if r.noBreakDetect {
		return r.runFallback(ctx)
	}
	return r.runWithBreakDetect(ctx)
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

				// stateWaitBreak: discard until the next break resyncs us.
			}
		}

		switch marker {
		case MarkerBreak:
			if state == stateReadData && frame.Length > 0 {
				r.emit(frame)
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
	state := stateWaitBreak
	lastData := time.Now()

	gap := r.GapThreshold
	if gap <= 0 {
		gap = DefaultGapThreshold
	}

	fmt.Println("Running in fallback mode (no BREAK detection)")
	fmt.Printf("Framing on packet length, resyncing on gaps over %v...\n", gap)

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
			// Idle. If a partial frame is open and the line has been quiet
			// longer than the threshold, close it out — this is what lets a
			// short-frame source sync at all.
			if state == stateReadData && frame.Length > 0 && now.Sub(lastData) >= gap {
				r.emit(frame)
				frame = Frame{}
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

		for i := 0; i < n; i++ {
			switch state {
			case stateWaitBreak:
				// Cold start: the first zero byte is our best guess at a start
				// code. If it was really a channel value we resync at the next
				// length boundary.
				if buf[i] == StartCodeDMX {
					frame.StartCode = StartCodeDMX
					state = stateReadData
				}

			case stateWaitStartCode:
				frame.StartCode = buf[i]
				if buf[i] == StartCodeDMX {
					state = stateReadData
				} else {
					state = stateWaitBreak
				}

			case stateReadData:
				frame.Channels[frame.Length] = buf[i]
				frame.Length++
				if frame.Length == MaxChannels {
					r.emit(frame)
					frame = Frame{}
					state = stateWaitStartCode
				}
			}
		}
	}
}
