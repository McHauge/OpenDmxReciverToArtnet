package dmx

import (
	"context"
	"fmt"
	"time"
)

type Receiver struct {
	port          *SerialPort
	Frames        chan Frame
	noBreakDetect bool
}

func NewReceiver(port *SerialPort, noBreakDetect bool) *Receiver {
	return &Receiver{
		port:          port,
		Frames:        make(chan Frame, 4),
		noBreakDetect: noBreakDetect,
	}
}

func (r *Receiver) Run(ctx context.Context) error {
	if r.noBreakDetect {
		return r.runFallback(ctx)
	}
	return r.runWithBreakDetect(ctx)
}

func (r *Receiver) runWithBreakDetect(ctx context.Context) error {
	breakCh := make(chan struct{}, 1)

	// Goroutine 1: BREAK event listener
	go func() {
		for {
			if err := r.port.WaitForBreak(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			select {
			case breakCh <- struct{}{}:
			default:
			}
		}
	}()

	// Goroutine 2: Data reader with BREAK awareness
	buf := make([]byte, 1024)
	var frame Frame
	state := stateWaitBreak

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Check for BREAK signal
		select {
		case <-breakCh:
			// If we were reading data, emit the completed frame
			if state == stateReadData && frame.Length > 0 {
				frame.Timestamp = time.Now()
				select {
				case r.Frames <- frame:
				default:
				}
			}
			frame = Frame{}
			state = stateWaitStartCode
		default:
		}

		if state == stateWaitBreak {
			// Just wait for BREAK, don't read
			select {
			case <-breakCh:
				state = stateWaitStartCode
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		n, err := r.port.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Read error — reset to wait for next BREAK
			state = stateWaitBreak
			continue
		}

		if n == 0 {
			continue
		}

		for i := 0; i < n; i++ {
			switch state {
			case stateWaitStartCode:
				frame.StartCode = buf[i]
				if buf[i] == StartCodeDMX {
					state = stateReadData
				} else {
					// Non-DMX start code, skip this frame
					state = stateWaitBreak
				}

			case stateReadData:
				if frame.Length < MaxChannels {
					frame.Channels[frame.Length] = buf[i]
					frame.Length++
				}
			}
		}

		// Check for BREAK again after read (may have arrived during read)
		select {
		case <-breakCh:
			if frame.Length > 0 {
				frame.Timestamp = time.Now()
				select {
				case r.Frames <- frame:
				default:
				}
			}
			frame = Frame{}
			state = stateWaitStartCode
		default:
		}
	}
}

// runFallback uses read timeouts to detect frame boundaries (no BREAK detection).
func (r *Receiver) runFallback(ctx context.Context) error {
	buf := make([]byte, 1024)
	var frame Frame
	state := stateWaitBreak
	lastRead := time.Now()

	fmt.Println("Running in fallback mode (no BREAK detection)")
	fmt.Println("Using read timeout gaps to detect frame boundaries...")

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		n, err := r.port.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}

		now := time.Now()

		if n == 0 {
			// Timeout with no data — if we had data, this gap is likely a BREAK
			if state == stateReadData && frame.Length > 0 && now.Sub(lastRead) > time.Millisecond {
				frame.Timestamp = now
				select {
				case r.Frames <- frame:
				default:
				}
				frame = Frame{}
				state = stateWaitBreak
			}
			continue
		}

		lastRead = now

		for i := 0; i < n; i++ {
			switch state {
			case stateWaitBreak:
				// In fallback mode, look for 0x00 bytes as potential BREAK/start code
				if buf[i] == 0x00 {
					frame.StartCode = StartCodeDMX
					state = stateReadData
				}

			case stateReadData:
				if frame.Length < MaxChannels {
					frame.Channels[frame.Length] = buf[i]
					frame.Length++
				}
			}
		}
	}
}
