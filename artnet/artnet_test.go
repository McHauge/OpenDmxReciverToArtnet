package artnet

import (
	"net"
	"testing"
	"time"

	"github.com/mc-ha/OpenDmxReciver/dmx"
)

// --- EncodeArtDmx tests ---

func TestEncodeArtDmxHeader(t *testing.T) {
	frame := dmx.Frame{
		StartCode: 0x00,
		Length:    4,
	}
	frame.Channels[0] = 10
	frame.Channels[1] = 20
	frame.Channels[2] = 30
	frame.Channels[3] = 40

	packet := EncodeArtDmx(frame, 1, 0, 0)

	// Verify header
	if string(packet[0:8]) != Header {
		t.Errorf("expected header %q, got %q", Header, string(packet[0:8]))
	}

	// Verify opcode (little-endian)
	if packet[8] != byte(OpDmx&0xFF) || packet[9] != byte(OpDmx>>8) {
		t.Errorf("expected opcode 0x%04x, got 0x%02x%02x", OpDmx, packet[9], packet[8])
	}

	// Verify sequence
	if packet[12] != 1 {
		t.Errorf("expected sequence 1, got %d", packet[12])
	}
}

func TestEncodeArtDmxUniverse(t *testing.T) {
	frame := dmx.Frame{Length: 2}
	frame.Channels[0] = 128
	frame.Channels[1] = 255

	packet := EncodeArtDmx(frame, 5, 12345, 0)

	// Universe 12345 = 0x3039
	// SubUni = low byte = 0x39 = 57
	// Net = high byte (7 bits) = 0x30 = 48
	if packet[14] != 0x39 {
		t.Errorf("expected SubUni 0x39, got 0x%02x", packet[14])
	}
	if packet[15] != 0x30 {
		t.Errorf("expected Net 0x30, got 0x%02x", packet[15])
	}
}

func TestEncodeArtDmxOddLengthPadded(t *testing.T) {
	frame := dmx.Frame{Length: 3}
	frame.Channels[0] = 1
	frame.Channels[1] = 2
	frame.Channels[2] = 3

	packet := EncodeArtDmx(frame, 1, 0, 0)

	// Odd length should be padded to even
	dataLen := int(packet[16])<<8 | int(packet[17])
	if dataLen%2 != 0 {
		t.Errorf("expected even data length, got %d", dataLen)
	}
	if dataLen < 3 {
		t.Errorf("expected data length >= 3, got %d", dataLen)
	}
}

func TestEncodeArtDmxMinLength(t *testing.T) {
	frame := dmx.Frame{Length: 1}
	frame.Channels[0] = 42

	packet := EncodeArtDmx(frame, 1, 0, 0)

	// Minimum length is 2
	dataLen := int(packet[16])<<8 | int(packet[17])
	if dataLen < 2 {
		t.Errorf("expected minimum data length 2, got %d", dataLen)
	}
}

func TestEncodeArtDmxChannelData(t *testing.T) {
	frame := dmx.Frame{Length: 512}
	for i := 0; i < 512; i++ {
		frame.Channels[i] = byte(i)
	}

	packet := EncodeArtDmx(frame, 1, 0, 0)

	for i := 0; i < 512; i++ {
		if packet[artDmxHeaderSize+i] != byte(i) {
			t.Errorf("channel %d: expected %d, got %d", i, byte(i), packet[artDmxHeaderSize+i])
		}
	}
}

// --- DecodeArtDmx tests ---

func TestDecodeArtDmxRoundTrip(t *testing.T) {
	original := dmx.Frame{Length: 256}
	for i := 0; i < 256; i++ {
		original.Channels[i] = byte(i % 256)
	}

	packet := EncodeArtDmx(original, 42, 100, 0)
	frame, universe, ok := DecodeArtDmx(packet)

	if !ok {
		t.Fatal("failed to decode valid ArtDmx packet")
	}
	if universe != 100 {
		t.Errorf("expected universe 100, got %d", universe)
	}
	// EncodeArtDmx always emits a full 512-channel packet regardless of
	// frame.Length, so a 256-channel frame decodes back as 512 with the
	// unused tail zero-padded.
	if frame.Length != dmx.MaxChannels {
		t.Errorf("expected length %d, got %d", dmx.MaxChannels, frame.Length)
	}
	for i := 0; i < 256; i++ {
		if frame.Channels[i] != byte(i%256) {
			t.Errorf("channel %d: expected %d, got %d", i, byte(i%256), frame.Channels[i])
		}
	}
	for i := 256; i < dmx.MaxChannels; i++ {
		if frame.Channels[i] != 0 {
			t.Errorf("channel %d: expected zero padding, got %d", i, frame.Channels[i])
		}
	}
}

func TestDecodeArtDmxTooShort(t *testing.T) {
	short := []byte{0x00, 0x00, 0x00}
	_, _, ok := DecodeArtDmx(short)
	if ok {
		t.Error("expected decode to fail on too-short packet")
	}
}

func TestDecodeArtDmxBadHeader(t *testing.T) {
	pkt := make([]byte, artDmxHeaderSize+2)
	copy(pkt[0:8], "BadNet\x00")
	pkt[8] = byte(OpDmx & 0xFF)
	pkt[9] = byte(OpDmx >> 8)

	_, _, ok := DecodeArtDmx(pkt)
	if ok {
		t.Error("expected decode to fail on bad header")
	}
}

func TestDecodeArtDmxWrongOpcode(t *testing.T) {
	pkt := make([]byte, artDmxHeaderSize+2)
	copy(pkt[0:8], Header)
	pkt[8] = 0x00 // wrong opcode
	pkt[9] = 0x20

	_, _, ok := DecodeArtDmx(pkt)
	if ok {
		t.Error("expected decode to fail on wrong opcode")
	}
}

func TestDecodeArtDmxClampsToMaxChannels(t *testing.T) {
	// Build a packet that claims to have more than 512 channels
	pkt := make([]byte, artDmxHeaderSize+1024)
	copy(pkt[0:8], Header)
	pkt[8] = byte(OpDmx & 0xFF)
	pkt[9] = byte(OpDmx >> 8)
	pkt[16] = 0x04 // length high byte
	pkt[17] = 0x00 // length low byte -> 1024

	frame, _, ok := DecodeArtDmx(pkt)
	if !ok {
		t.Fatal("expected decode to succeed")
	}
	if frame.Length > dmx.MaxChannels {
		t.Errorf("expected length clamped to %d, got %d", dmx.MaxChannels, frame.Length)
	}
}

func TestDecodeArtDmxTimestamp(t *testing.T) {
	before := time.Now()
	frame, _, ok := DecodeArtDmx(EncodeArtDmx(dmx.Frame{Length: 2}, 1, 0, 0))
	after := time.Now()

	if !ok {
		t.Fatal("decode failed")
	}
	if frame.Timestamp.Before(before) || frame.Timestamp.After(after) {
		t.Error("expected timestamp to be set to current time")
	}
}

// --- EncodeArtPollReply tests ---

func TestEncodeArtPollReplyStructure(t *testing.T) {
	ip := net.ParseIP("192.168.1.100")
	reply := EncodeArtPollReply(ip, 1, "TestNode")

	if len(reply) != artPollReplySize {
		t.Errorf("expected reply length %d, got %d", artPollReplySize, len(reply))
	}

	// Header
	if string(reply[0:8]) != Header {
		t.Errorf("expected header %q, got %q", Header, string(reply[0:8]))
	}

	// Opcode
	if reply[8] != byte(OpPollReply&0xFF) || reply[9] != byte(OpPollReply>>8) {
		t.Errorf("expected opcode 0x%04x", OpPollReply)
	}

	// IP
	if !net.IP(reply[10:14]).Equal(ip) {
		t.Errorf("expected IP %v, got %v", ip, net.IP(reply[10:14]))
	}

	// Port
	if reply[14] != byte(Port&0xFF) || reply[15] != byte(Port>>8) {
		t.Errorf("expected port %d", Port)
	}

	// ShortName
	expectedName := "TestNode"
	actualName := string(reply[26 : 26+len(expectedName)])
	if actualName != expectedName {
		t.Errorf("expected short name %q, got %q", expectedName, actualName)
	}
}

func TestEncodeArtPollReplyShortNameTruncated(t *testing.T) {
	longName := "ThisIsAVeryLongNodeNameXX" // longer than 17 chars
	reply := EncodeArtPollReply(net.IPv4(127, 0, 0, 1), 0, longName)

	nameInPacket := string(reply[26 : 26+maxShortNameLen])
	if len(nameInPacket) > maxShortNameLen {
		t.Errorf("short name not truncated: length %d", len(nameInPacket))
	}
}

// --- IsArtPoll tests ---

func TestIsArtPollValid(t *testing.T) {
	pkt := make([]byte, 18)
	copy(pkt[0:8], Header)
	pkt[8] = byte(OpPoll & 0xFF)
	pkt[9] = byte(OpPoll >> 8)

	if !IsArtPoll(pkt) {
		t.Error("expected IsArtPoll to return true for valid ArtPoll")
	}
}

func TestIsArtPollWrongOpcode(t *testing.T) {
	pkt := make([]byte, 18)
	copy(pkt[0:8], Header)
	pkt[8] = byte(OpDmx & 0xFF)
	pkt[9] = byte(OpDmx >> 8)

	if IsArtPoll(pkt) {
		t.Error("expected IsArtPoll to return false for ArtDmx opcode")
	}
}

func TestIsArtPollTooShort(t *testing.T) {
	short := []byte{0x00, 0x00}
	if IsArtPoll(short) {
		t.Error("expected IsArtPoll to return false for too-short packet")
	}
}

func TestIsArtPollBadHeader(t *testing.T) {
	pkt := make([]byte, 18)
	copy(pkt[0:8], "BadNet\x00")
	pkt[8] = byte(OpPoll & 0xFF)
	pkt[9] = byte(OpPoll >> 8)

	if IsArtPoll(pkt) {
		t.Error("expected IsArtPoll to return false for bad header")
	}
}

// --- Full round-trip with multiple universes ---

func TestEncodeDecodeMultipleUniverses(t *testing.T) {
	universes := []uint16{0, 1, 255, 32767}

	for _, uni := range universes {
		frame := dmx.Frame{Length: 10}
		for i := 0; i < 10; i++ {
			frame.Channels[i] = byte(i * 25)
		}

		packet := EncodeArtDmx(frame, 1, uni, 0)
		decoded, decodedUni, ok := DecodeArtDmx(packet)

		if !ok {
			t.Errorf("universe %d: decode failed", uni)
			continue
		}
		if decodedUni != uni {
			t.Errorf("universe %d: decoded as %d", uni, decodedUni)
		}
		for i := 0; i < 10; i++ {
			if decoded.Channels[i] != frame.Channels[i] {
				t.Errorf("universe %d, channel %d: expected %d, got %d", uni, i, frame.Channels[i], decoded.Channels[i])
			}
		}
	}
}