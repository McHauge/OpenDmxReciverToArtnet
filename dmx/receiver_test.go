package dmx

import (
	"context"
	"errors"
	"testing"
	"time"
)

// step is one scripted ReadChunk result.
type step struct {
	data   []byte
	marker Marker
	err    error
}

// fakePort replays a script of ReadChunk results, then blocks on idle timeouts
// so the receiver loop behaves as it would against a quiet line.
type fakePort struct {
	steps  []step
	i      int
	closed bool
}

func (f *fakePort) ReadChunk(buf []byte) (int, Marker, error) {
	if f.i >= len(f.steps) {
		// Script exhausted: look like an idle line rather than EOF.
		time.Sleep(time.Millisecond)
		return 0, MarkerNone, nil
	}
	s := f.steps[f.i]
	f.i++
	if s.err != nil {
		return 0, MarkerNone, s.err
	}
	n := copy(buf, s.data)
	return n, s.marker, nil
}

func (f *fakePort) Close() error { f.closed = true; return nil }

// collect runs a receiver until it stops, returning the frames it emitted.
func collect(t *testing.T, port Port, noBreakDetect bool, timeout time.Duration) []Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	r := NewReceiver(port, noBreakDetect)
	done := make(chan struct{})
	go func() { defer close(done); _ = r.Run(ctx) }()

	var got []Frame
	for {
		select {
		case f := <-r.Frames:
			got = append(got, f)
		case <-done:
			// Drain anything already buffered.
			for {
				select {
				case f := <-r.Frames:
					got = append(got, f)
				default:
					return got
				}
			}
		}
	}
}

// channels builds n bytes with a recognisable ramp.
func channels(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i%255 + 1) // avoid 0 so start codes stay distinguishable
	}
	return b
}

func TestReceiverBreakDetectEmitsFrame(t *testing.T) {
	body := append([]byte{StartCodeDMX}, channels(MaxChannels)...)
	port := &fakePort{steps: []step{
		{marker: MarkerBreak},             // opening break
		{data: body, marker: MarkerBreak}, // full frame, closed by the next break
		{err: errors.New("port closed")},  // stop the loop
	}}

	got := collect(t, port, false, 2*time.Second)

	if len(got) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(got))
	}
	if got[0].Length != MaxChannels {
		t.Errorf("Length = %d, want %d", got[0].Length, MaxChannels)
	}
	if got[0].StartCode != StartCodeDMX {
		t.Errorf("StartCode = %#x, want 0", got[0].StartCode)
	}
	want := channels(MaxChannels)
	for i, w := range want {
		if got[0].Channels[i] != w {
			t.Fatalf("channel %d = %d, want %d", i, got[0].Channels[i], w)
		}
	}
}

func TestReceiverBreakSplitAcrossReads(t *testing.T) {
	// The frame body arrives in three reads; only the last carries the break.
	body := append([]byte{StartCodeDMX}, channels(300)...)
	port := &fakePort{steps: []step{
		{marker: MarkerBreak},
		{data: body[:100]},
		{data: body[100:200]},
		{data: body[200:], marker: MarkerBreak},
		{err: errors.New("stop")},
	}}

	got := collect(t, port, false, 2*time.Second)
	if len(got) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(got))
	}
	if got[0].Length != 300 {
		t.Errorf("Length = %d, want 300", got[0].Length)
	}
}

func TestReceiverRejectsNonZeroStartCode(t *testing.T) {
	// An RDM packet (start code 0xCC) must not be emitted as DMX.
	port := &fakePort{steps: []step{
		{marker: MarkerBreak},
		{data: append([]byte{0xCC}, channels(50)...), marker: MarkerBreak},
		{err: errors.New("stop")},
	}}

	if got := collect(t, port, false, 2*time.Second); len(got) != 0 {
		t.Fatalf("expected no frames for start code 0xCC, got %d", len(got))
	}
}

func TestReceiverMarkerErrorDiscardsFrame(t *testing.T) {
	port := &fakePort{steps: []step{
		{marker: MarkerBreak},
		{data: append([]byte{StartCodeDMX}, channels(100)...), marker: MarkerError},
		{data: channels(50), marker: MarkerBreak}, // still desynced, discarded
		{err: errors.New("stop")},
	}}

	if got := collect(t, port, false, 2*time.Second); len(got) != 0 {
		t.Fatalf("expected no frames after a line error, got %d", len(got))
	}
}

func TestReceiverCapsOversizedFrame(t *testing.T) {
	// More than 512 channels between breaks must not panic or overflow.
	port := &fakePort{steps: []step{
		{marker: MarkerBreak},
		{data: append([]byte{StartCodeDMX}, channels(MaxChannels+200)...), marker: MarkerBreak},
		{err: errors.New("stop")},
	}}

	got := collect(t, port, false, 2*time.Second)
	if len(got) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(got))
	}
	if got[0].Length != MaxChannels {
		t.Errorf("Length = %d, want %d (capped)", got[0].Length, MaxChannels)
	}
}

// The bug this port was built around: a continuous source produces no idle
// timeout, so the old fallback accumulated to 512 and then silently dropped
// every subsequent byte, emitting nothing. Length anchoring fixes it.
func TestReceiverFallbackEmitsFromContinuousStream(t *testing.T) {
	// Three back-to-back packets, no gaps and no markers anywhere.
	var stream []byte
	for i := 0; i < 3; i++ {
		stream = append(stream, StartCodeDMX)
		stream = append(stream, channels(MaxChannels)...)
	}
	// Deliver it in 200-byte reads, so packet boundaries never align with reads.
	var steps []step
	for off := 0; off < len(stream); off += 200 {
		end := min(off+200, len(stream))
		steps = append(steps, step{data: stream[off:end]})
	}
	steps = append(steps, step{err: errors.New("stop")})

	got := collect(t, &fakePort{steps: steps}, true, 2*time.Second)

	if len(got) != 3 {
		t.Fatalf("expected 3 frames from a continuous stream, got %d", len(got))
	}
	for i, f := range got {
		if f.Length != MaxChannels {
			t.Errorf("frame %d: Length = %d, want %d", i, f.Length, MaxChannels)
		}
	}
}

func TestReceiverFallbackEmitsShortFrameOnGap(t *testing.T) {
	port := &fakePort{steps: []step{
		{data: append([]byte{StartCodeDMX}, channels(24)...)},
		// Idle read: the gap closes the short frame out.
		{},
		{err: errors.New("stop")},
	}}

	r := NewReceiver(port, true)
	r.GapThreshold = time.Nanosecond // any gap counts

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	select {
	case f := <-r.Frames:
		if f.Length != 24 {
			t.Errorf("Length = %d, want 24", f.Length)
		}
	case <-ctx.Done():
		t.Fatal("no frame emitted on idle gap")
	}
}

func TestReceiverReturnsReadError(t *testing.T) {
	sentinel := errors.New("adapter unplugged")
	r := NewReceiver(&fakePort{steps: []step{{err: sentinel}}}, false)

	err := r.Run(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want %v — a persistent read error must surface, not spin", err, sentinel)
	}
}

func TestReceiverContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := NewReceiver(&fakePort{}, false)

	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
