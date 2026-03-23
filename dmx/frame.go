package dmx

import "time"

const (
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
)
