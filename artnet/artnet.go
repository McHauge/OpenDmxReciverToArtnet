package artnet

import (
	"encoding/binary"
	"net"
	"time"

	"github.com/mc-ha/OpenDmxReciver/dmx"
)

const (
	Port    = 6454
	Header  = "Art-Net\x00"
	ProtVer = 14

	OpPoll      uint16 = 0x2000
	OpPollReply uint16 = 0x2100
	OpDmx       uint16 = 0x5000

	artDmxHeaderSize   = 18
	artPollReplySize   = 239
	styleNode          = 0x00
	maxShortNameLen    = 18
	maxLongNameLen     = 64
)

// EncodeArtDmx builds an ArtDmx packet from a DMX frame.
func EncodeArtDmx(frame dmx.Frame, seq byte, universe uint16, physical byte) []byte {
	length := frame.Length
	if length < 2 {
		length = 2
	}
	if length%2 != 0 {
		length++
	}

	buf := make([]byte, artDmxHeaderSize+length)

	// Header "Art-Net\0"
	copy(buf[0:8], Header)

	// OpCode — little-endian
	binary.LittleEndian.PutUint16(buf[8:10], OpDmx)

	// Protocol version — big-endian
	buf[10] = 0x00
	buf[11] = byte(ProtVer)

	// Sequence
	buf[12] = seq

	// Physical port
	buf[13] = physical

	// SubUni (low byte of universe) and Net (high byte, 7 bits)
	buf[14] = byte(universe & 0xFF)
	buf[15] = byte((universe >> 8) & 0x7F)

	// Length — big-endian
	binary.BigEndian.PutUint16(buf[16:18], uint16(length))

	// Channel data (Channels array is zero-initialized, so padding bytes are already 0)
	copy(buf[18:], frame.Channels[:length])

	return buf
}

// EncodeArtPollReply builds a 239-byte ArtPollReply packet.
func EncodeArtPollReply(localIP net.IP, universe uint16, shortName string) []byte {
	buf := make([]byte, artPollReplySize)

	// Header "Art-Net\0"
	copy(buf[0:8], Header)

	// OpCode — little-endian
	binary.LittleEndian.PutUint16(buf[8:10], OpPollReply)

	// IP address (4 bytes at offset 10)
	ip4 := localIP.To4()
	if ip4 != nil {
		copy(buf[10:14], ip4)
	}

	// Port — little-endian
	binary.LittleEndian.PutUint16(buf[14:16], Port)

	// Version (firmware) — high, low
	buf[16] = 0x00
	buf[17] = 0x01

	// NetSwitch (bits 14-8 of universe)
	buf[18] = byte((universe >> 8) & 0x7F)

	// SubSwitch (bits 7-4 of universe)
	buf[19] = byte((universe >> 4) & 0x0F)

	// OEM — 0x0000
	buf[20] = 0x00
	buf[21] = 0x00

	// UBEA version
	buf[22] = 0x00

	// Status1
	buf[23] = 0x00

	// ESTA manufacturer — 0x0000
	buf[24] = 0x00
	buf[25] = 0x00

	// ShortName (18 bytes at offset 26)
	if len(shortName) > maxShortNameLen-1 {
		shortName = shortName[:maxShortNameLen-1]
	}
	copy(buf[26:26+maxShortNameLen], shortName)

	// LongName (64 bytes at offset 44)
	longName := "OpenDmxReciver Art-Net Node"
	copy(buf[44:44+maxLongNameLen], longName)

	// NodeReport (64 bytes at offset 108) — leave zeroed

	// NumPortsHi, NumPortsLo (offset 172-173)
	buf[172] = 0x00
	buf[173] = 0x01

	// PortTypes (4 bytes at offset 174) — port 0 is input (DMX into Art-Net)
	buf[174] = 0x80 // can output Art-Net data (bit 7)

	// GoodInput (4 bytes at offset 178) — data received
	buf[178] = 0x80

	// GoodOutput (4 bytes at offset 182) — zeroed

	// SwIn (4 bytes at offset 186) — universe low nibble
	buf[186] = byte(universe & 0x0F)

	// SwOut (4 bytes at offset 190) — zeroed

	// Style (offset 200)
	buf[200] = styleNode

	// MAC address (6 bytes at offset 201) — leave zeroed

	return buf
}

// DecodeArtDmx parses an ArtDmx packet. Returns the frame, universe, and whether decoding succeeded.
func DecodeArtDmx(data []byte) (dmx.Frame, uint16, bool) {
	if len(data) < artDmxHeaderSize {
		return dmx.Frame{}, 0, false
	}
	if string(data[0:8]) != Header {
		return dmx.Frame{}, 0, false
	}
	opcode := binary.LittleEndian.Uint16(data[8:10])
	if opcode != OpDmx {
		return dmx.Frame{}, 0, false
	}

	universe := uint16(data[14]) | uint16(data[15])<<8
	length := int(binary.BigEndian.Uint16(data[16:18]))
	if length > dmx.MaxChannels {
		length = dmx.MaxChannels
	}
	if len(data)-artDmxHeaderSize < length {
		length = len(data) - artDmxHeaderSize
	}

	var frame dmx.Frame
	copy(frame.Channels[:length], data[artDmxHeaderSize:artDmxHeaderSize+length])
	frame.Length = length
	frame.Timestamp = time.Now()

	return frame, universe, true
}

// IsArtPoll checks if a received packet is an ArtPoll request.
func IsArtPoll(data []byte) bool {
	if len(data) < 10 {
		return false
	}
	if string(data[0:8]) != Header {
		return false
	}
	opcode := uint16(data[8]) | uint16(data[9])<<8
	return opcode == OpPoll
}
