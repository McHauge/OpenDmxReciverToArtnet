package merge

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mc-ha/OpenDmxReciver/dmx"
)

// --- Helper to drain channels ---

func drainOutput(m *Merger) {
	for {
		select {
		case <-m.Output:
		default:
			return
		}
	}
}

func drainEvents(m *Merger) {
	for {
		select {
		case <-m.Events:
		default:
			return
		}
	}
}

// --- AddMapping tests ---

func TestAddMapping(t *testing.T) {
	m := NewMerger(5 * time.Second)

	m.AddMapping("src1", 0)

	m.mu.Lock()
	s, ok := m.sources["src1"]
	m.mu.Unlock()

	if !ok {
		t.Fatal("source not registered")
	}
	if s.outputUni != 0 {
		t.Errorf("expected output universe 0, got %d", s.outputUni)
	}
}

func TestAddMappingMultipleSources(t *testing.T) {
	m := NewMerger(5 * time.Second)

	m.AddMapping("src1", 0)
	m.AddMapping("src2", 0)
	m.AddMapping("src3", 1)

	m.mu.Lock()
	count := len(m.sources)
	m.mu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 sources, got %d", count)
	}
}

// --- Update tests ---

func TestUpdateBasic(t *testing.T) {
	m := NewMerger(0) // no expiry
	m.AddMapping("local", 0)

	frame := dmx.Frame{Length: 2}
	frame.Channels[0] = 100
	frame.Channels[1] = 200

	m.Update("local", frame)

	select {
	case out := <-m.Output:
		if out.Universe != 0 {
			t.Errorf("expected universe 0, got %d", out.Universe)
		}
		if out.Frame.Channels[0] != 100 {
			t.Errorf("expected channel 0 = 100, got %d", out.Frame.Channels[0])
		}
		if out.Frame.Channels[1] != 200 {
			t.Errorf("expected channel 1 = 200, got %d", out.Frame.Channels[1])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for output")
	}
}

func TestUpdateUnregisteredSourceIgnored(t *testing.T) {
	m := NewMerger(0)
	// Do NOT register "unknown"

	frame := dmx.Frame{Length: 1}
	frame.Channels[0] = 255

	m.Update("unknown", frame)

	select {
	case <-m.Output:
		t.Error("expected no output for unregistered source")
	case <-time.After(50 * time.Millisecond):
		// correct: no output
	}
}

// --- HTP Merge tests ---

func TestHTPBasic(t *testing.T) {
	m := NewMerger(0)
	m.AddMapping("srcA", 0)
	m.AddMapping("srcB", 0)

	frameA := dmx.Frame{Length: 3}
	frameA.Channels[0] = 100
	frameA.Channels[1] = 50
	frameA.Channels[2] = 200

	frameB := dmx.Frame{Length: 3}
	frameB.Channels[0] = 150 // higher on ch0
	frameB.Channels[1] = 25  // lower on ch1
	frameB.Channels[2] = 180 // lower on ch2

	m.Update("srcA", frameA)
	drainOutput(m)
	m.Update("srcB", frameB)

	select {
	case out := <-m.Output:
		// HTP: ch0=150 (B wins), ch1=50 (A wins), ch2=200 (A wins)
		if out.Frame.Channels[0] != 150 {
			t.Errorf("ch0: expected 150, got %d", out.Frame.Channels[0])
		}
		if out.Frame.Channels[1] != 50 {
			t.Errorf("ch1: expected 50, got %d", out.Frame.Channels[1])
		}
		if out.Frame.Channels[2] != 200 {
			t.Errorf("ch2: expected 200, got %d", out.Frame.Channels[2])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for output")
	}
}

func TestHTPThreeSources(t *testing.T) {
	m := NewMerger(0)
	m.AddMapping("s1", 0)
	m.AddMapping("s2", 0)
	m.AddMapping("s3", 0)

	f1 := dmx.Frame{Length: 2}
	f1.Channels[0] = 100
	f1.Channels[1] = 30

	f2 := dmx.Frame{Length: 2}
	f2.Channels[0] = 50
	f2.Channels[1] = 200

	f3 := dmx.Frame{Length: 2}
	f3.Channels[0] = 180
	f3.Channels[1] = 150

	m.Update("s1", f1)
	m.Update("s2", f2)
	m.Update("s3", f3)

	// Collect all available outputs (up to 3, with timeout)
	var outputs []OutputFrame
	for i := 0; i < 3; i++ {
		select {
		case out := <-m.Output:
			outputs = append(outputs, out)
		case <-time.After(100 * time.Millisecond):
			goto done
		}
	}
done:
	if len(outputs) == 0 {
		t.Fatal("no outputs received")
	}

	// The last output should be the merged result of all three sources
	out := outputs[len(outputs)-1]

	// HTP: ch0=max(100,50,180)=180, ch1=max(30,200,150)=200
	if out.Frame.Channels[0] != 180 {
		t.Errorf("ch0: expected 180, got %d", out.Frame.Channels[0])
	}
	if out.Frame.Channels[1] != 200 {
		t.Errorf("ch1: expected 200, got %d", out.Frame.Channels[1])
	}
}

//

func TestMultipleOutputUniverses(t *testing.T) {
	m := NewMerger(0)
	m.AddMapping("srcA", 0)
	m.AddMapping("srcB", 1)

	frameA := dmx.Frame{Length: 1}
	frameA.Channels[0] = 100

	frameB := dmx.Frame{Length: 1}
	frameB.Channels[0] = 200

	m.Update("srcA", frameA)
	outA := <-m.Output

	m.Update("srcB", frameB)
	outB := <-m.Output

	if outA.Universe != 0 {
		t.Errorf("expected universe 0, got %d", outA.Universe)
	}
	if outA.Frame.Channels[0] != 100 {
		t.Errorf("expected 100, got %d", outA.Frame.Channels[0])
	}

	if outB.Universe != 1 {
		t.Errorf("expected universe 1, got %d", outB.Universe)
	}
	if outB.Frame.Channels[0] != 200 {
		t.Errorf("expected 200, got %d", outB.Frame.Channels[0])
	}

	// Verify srcA data does NOT leak into universe 1
	if outB.Frame.Channels[0] == 100 {
		t.Error("srcA data leaked into universe 1")
	}
}

// --- Source events ---

func TestSourceConnectedEvent(t *testing.T) {
	m := NewMerger(0)
	m.AddMapping("test", 0)

	frame := dmx.Frame{Length: 1}
	frame.Channels[0] = 42

	m.Update("test", frame)
	drainOutput(m)

	select {
	case evt := <-m.Events:
		if evt.ID != "test" {
			t.Errorf("expected event ID test, got %s", evt.ID)
		}
		if !evt.Connected {
			t.Error("expected connected event")
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("timed out waiting for connected event")
	}
}

func TestSourceConnectedEventOnlyOnce(t *testing.T) {
	m := NewMerger(0)
	m.AddMapping("test", 0)

	frame := dmx.Frame{Length: 1}
	frame.Channels[0] = 10

	// First update should fire connected event
	m.Update("test", frame)
	drainOutput(m)
	drainEvents(m)

	// Second update should NOT fire another connected event
	m.Update("test", frame)
	drainOutput(m)

	select {
	case evt := <-m.Events:
		t.Errorf("expected no second connected event, got %+v", evt)
	case <-time.After(50 * time.Millisecond):
		// correct: no event
	}
}

// --- Source expiry ---

func TestSourceExpiry(t *testing.T) {
	m := NewMerger(100 * time.Millisecond)
	m.AddMapping("test", 0)

	frame := dmx.Frame{Length: 1}
	frame.Channels[0] = 255

	m.Update("test", frame)
	drainOutput(m)
	drainEvents(m)

	// Wait for source to expire
	time.Sleep(300 * time.Millisecond)

	// Trigger expiry manually
	m.expireSources()

	// Should get a disconnected event
	select {
	case evt := <-m.Events:
		if evt.ID != "test" {
			t.Errorf("expected event ID test, got %s", evt.ID)
		}
		if evt.Connected {
			t.Error("expected disconnected event")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for disconnected event")
	}

	// Verify source state is cleared
	m.mu.Lock()
	s := m.sources["test"]
	m.mu.Unlock()

	if s.active {
		t.Error("expected source to be inactive after expiry")
	}
}

func TestSourceNotExpiredWhenActive(t *testing.T) {
	m := NewMerger(1 * time.Second) // long timeout
	m.AddMapping("test", 0)

	frame := dmx.Frame{Length: 1}
	frame.Channels[0] = 128

	m.Update("test", frame)
	drainOutput(m)
	drainEvents(m)

	// Expire check before timeout should do nothing
	m.expireSources()

	select {
	case evt := <-m.Events:
		t.Errorf("expected no disconnected event, got %+v", evt)
	case <-time.After(50 * time.Millisecond):
		// correct: no event
	}

	m.mu.Lock()
	s := m.sources["test"]
	m.mu.Unlock()

	if !s.active {
		t.Error("expected source to remain active")
	}
}

func TestZeroTimeoutNeverExpires(t *testing.T) {
	m := NewMerger(0) // zero timeout = never expire
	m.AddMapping("test", 0)

	frame := dmx.Frame{Length: 1}
	frame.Channels[0] = 200

	m.Update("test", frame)
	drainOutput(m)
	drainEvents(m)

	// Even after long wait, source should not expire
	time.Sleep(100 * time.Millisecond)
	m.expireSources()

	m.mu.Lock()
	s := m.sources["test"]
	m.mu.Unlock()

	if !s.active {
		t.Error("expected source to stay active with zero timeout")
	}
}

// --- Run (expiry ticker) ---

func TestRunStopsOnCancel(t *testing.T) {
	m := NewMerger(100 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.Run(ctx)
	}()

	cancel()
	wg.Wait() // should return immediately after cancel
}

func TestRunWithZeroTimeoutBlocksUntilCancel(t *testing.T) {
	m := NewMerger(0)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.Run(ctx)
	}()

	wg.Wait()
}

// --- computeHTP ---

func TestComputeHTPEmptyUniverse(t *testing.T) {
	m := NewMerger(0)

	// No sources for universe 99
	m.mu.Lock()
	result := m.computeHTP(99)
	m.mu.Unlock()

	if result.Length != 0 {
		t.Errorf("expected length 0 for empty universe, got %d", result.Length)
	}
}

func TestComputeHTPAllChannelsZeroAfterExpiry(t *testing.T) {
	m := NewMerger(100 * time.Millisecond)
	m.AddMapping("src", 0)

	frame := dmx.Frame{Length: 1}
	frame.Channels[0] = 255

	m.Update("src", frame)
	drainOutput(m)
	drainEvents(m)

	// Expire source
	time.Sleep(200 * time.Millisecond)
	m.expireSources()
	drainEvents(m)
	drainOutput(m)

	m.mu.Lock()
	result := m.computeHTP(0)
	m.mu.Unlock()

	if result.Length != 0 {
		t.Errorf("expected length 0 after all sources expired, got %d", result.Length)
	}
}

// --- Output channel replace behavior ---

func TestOutputChannelReplacesStaleValue(t *testing.T) {
	// Create merger with small output buffer and fill it
	m := NewMerger(0)
	m.AddMapping("src", 0)

	// Fill the buffer (size 4)
	for i := 0; i < 4; i++ {
		frame := dmx.Frame{Length: 1}
		frame.Channels[0] = byte(i * 50)
		m.Update("src", frame)
		<-m.Output // consume immediately to keep channel available
	}

	// Now send without consuming to fill buffer
	for i := 0; i < 4; i++ {
		frame := dmx.Frame{Length: 1}
		frame.Channels[0] = byte(i * 10)
		m.Update("src", frame)
	}

	// Send one more, should replace the oldest
	frame := dmx.Frame{Length: 1}
	frame.Channels[0] = 255
	m.Update("src", frame)

	// Drain and check the last value is 255
	found := false
	timeout := time.After(100 * time.Millisecond)
	for i := 0; i < 4; i++ {
		select {
		case out := <-m.Output:
			if out.Frame.Channels[0] == 255 {
				found = true
			}
		case <-timeout:
			goto done
		}
	}
done:
	if !found {
		t.Error("expected to find the latest value (255) in output")
	}
}