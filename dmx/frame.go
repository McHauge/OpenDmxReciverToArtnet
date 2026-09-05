package dmx

import "time"

const (
	// dmxBaudRate is the DMX512 line rate. Note this is not a standard POSIX
	// baud constant, which is why each platform needs a custom-rate path.
	dmxBaudRate = 250000

	MaxChannels  = 512
	StartCodeDMX = 0x00
)

type Frame struct {
	StartCode byte
	Channels  [MaxChannels]byte
	Length    int
	Timestamp time.Time
}

type rxState int

const (
	stateWaitBreak rxState = iota
	stateWaitStartCode
	stateReadData
	// stateSkipTrailer consumes the stray byte that the break leaves in the
	// stream on platforms where the UART delivers it as data rather than as a
	// line event. See breakFromShortRead.
	stateSkipTrailer
)
