package artnet

import (
	"net"
	"testing"

	"github.com/mc-ha/OpenDmxReciver/dmx"
)

// Go does not set SO_BROADCAST on its own, and Linux rejects a sendto to a
// broadcast address without it. 255.255.255.255 is the default Art-Net
// destination, so this is a genuine regression test there. Darwin and Windows
// permit the send either way, so on those platforms it only asserts that the
// socket setup itself did not break.
func TestBroadcastSendIsPermitted(t *testing.T) {
	conn, err := listenBroadcastUDP(0)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	defer conn.Close()

	dest := &net.UDPAddr{IP: net.IPv4bcast, Port: Port}
	packet := EncodeArtDmx(dmx.Frame{Length: dmx.MaxChannels}, 1, 0, 0)

	if _, err := conn.WriteToUDP(packet, dest); err != nil {
		t.Fatalf("broadcast send to %s failed: %v\n"+
			"SO_BROADCAST is probably not set on the socket", dest, err)
	}
}

func TestDetectLocalIPForBroadcastDest(t *testing.T) {
	// Must not fall back to 0.0.0.0: that address ends up in every
	// ArtPollReply, making the node undiscoverable.
	ip := detectLocalIP(&net.UDPAddr{IP: net.IPv4bcast, Port: Port})
	if ip == nil || ip.IsUnspecified() {
		t.Fatalf("detectLocalIP returned %v for a broadcast destination", ip)
	}
}
