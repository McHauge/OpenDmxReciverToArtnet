package merge

import (
	"context"
	"sync"
	"time"

	"github.com/mc-ha/OpenDmxReciver/dmx"
)

// SourceID identifies a DMX source (e.g., "local", "artnet:2:192.168.1.5").
type SourceID string

// OutputFrame is a merged DMX frame tagged with its output universe.
type OutputFrame struct {
	Frame    dmx.Frame
	Universe uint16
}

// SourceEvent signals when a merge source connects or disconnects.
type SourceEvent struct {
	ID        SourceID
	Connected bool // true = first data received, false = timed out
}

type sourceState struct {
	frame      dmx.Frame
	outputUni  uint16
	lastSeen   time.Time
	active     bool // true if currently contributing data
}

// Merger applies HTP (Highest Takes Precedence) merging across multiple DMX sources.
type Merger struct {
	mu      sync.Mutex
	sources map[SourceID]*sourceState
	timeout time.Duration
	Output  chan OutputFrame
	Events  chan SourceEvent
}

// NewMerger creates a merger. If timeout is 0, sources never expire.
// Call Run() to start the expiry ticker.
func NewMerger(timeout time.Duration) *Merger {
	return &Merger{
		sources: make(map[SourceID]*sourceState),
		timeout: timeout,
		Output:  make(chan OutputFrame, 4),
		Events:  make(chan SourceEvent, 8),
	}
}

// AddMapping registers a source and the output universe it contributes to.
func (m *Merger) AddMapping(id SourceID, outputUniverse uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sources[id] = &sourceState{outputUni: outputUniverse}
}

// Update stores a new frame for the given source and pushes the merged result
// for the affected output universe.
func (m *Merger) Update(id SourceID, frame dmx.Frame) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sources[id]
	if !ok {
		// Dynamic source (Art-Net input not pre-registered) — ignore
		return
	}
	wasActive := s.active
	s.frame = frame
	s.lastSeen = time.Now()
	s.active = true

	if !wasActive {
		select {
		case m.Events <- SourceEvent{ID: id, Connected: true}:
		default:
		}
	}

	merged := m.computeHTP(s.outputUni)
	out := OutputFrame{Frame: merged, Universe: s.outputUni}

	// Non-blocking send; replace stale value if consumer is slow.
	select {
	case m.Output <- out:
	default:
		select {
		case <-m.Output:
		default:
		}
		m.Output <- out
	}
}

// Run starts a background ticker that expires timed-out sources and recomputes
// affected outputs. It blocks until the context is cancelled.
func (m *Merger) Run(ctx context.Context) {
	if m.timeout <= 0 {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.expireSources()
		}
	}
}

// expireSources checks for timed-out sources and recomputes affected universes.
func (m *Merger) expireSources() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	affected := make(map[uint16]bool)

	for id, s := range m.sources {
		if s.lastSeen.IsZero() {
			continue
		}
		if now.Sub(s.lastSeen) > m.timeout {
			s.frame = dmx.Frame{}
			s.lastSeen = time.Time{}
			s.active = false
			affected[s.outputUni] = true
			select {
			case m.Events <- SourceEvent{ID: id, Connected: false}:
			default:
			}
		}
	}

	for uni := range affected {
		merged := m.computeHTP(uni)
		out := OutputFrame{Frame: merged, Universe: uni}
		select {
		case m.Output <- out:
		default:
		}
	}
}

// computeHTP merges all active sources for a given output universe using HTP.
// Must be called with m.mu held.
func (m *Merger) computeHTP(outputUniverse uint16) dmx.Frame {
	var result dmx.Frame
	maxLen := 0

	for _, s := range m.sources {
		if s.outputUni != outputUniverse {
			continue
		}
		if s.lastSeen.IsZero() {
			continue
		}
		if s.frame.Length > maxLen {
			maxLen = s.frame.Length
		}
		for i := 0; i < s.frame.Length; i++ {
			if s.frame.Channels[i] > result.Channels[i] {
				result.Channels[i] = s.frame.Channels[i]
			}
		}
	}

	result.Length = maxLen
	result.Timestamp = time.Now()
	return result
}
