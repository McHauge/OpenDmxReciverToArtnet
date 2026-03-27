package artnet

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/mc-ha/OpenDmxReciver/dmx"
)

// ReceivedFrame is an ArtDmx frame received from the network.
type ReceivedFrame struct {
	Frame    dmx.Frame
	Universe uint16
	Source   net.IP
}

// Node sends ArtDmx packets and responds to ArtPoll discovery.
type Node struct {
	conn           *net.UDPConn
	dest           *net.UDPAddr
	universe       uint16
	seq            byte
	localIP        net.IP
	ReceivedDmx    chan ReceivedFrame
	outputUniverses map[uint16]bool // universes we send on (for loopback filtering)
	debug           bool
}

// SetDebug enables verbose logging of received Art-Net packets.
func (n *Node) SetDebug(enabled bool) {
	n.debug = enabled
}

// NewNode creates an Art-Net node bound to the given address.
// bindAddr may be empty for auto-detection. dest is the target IP (broadcast or unicast).
func NewNode(bindAddr string, dest string, universe uint16) (*Node, error) {
	destAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", dest, Port))
	if err != nil {
		return nil, fmt.Errorf("resolve destination: %w", err)
	}

	// Determine local bind address
	var bindIP net.IP
	if bindAddr != "" {
		bindIP = net.ParseIP(bindAddr)
		if bindIP == nil {
			return nil, fmt.Errorf("invalid bind address: %s", bindAddr)
		}
	}

	// Bind to 0.0.0.0 so we can receive broadcast Art-Net packets.
	// The bindAddr is used for ArtPollReply and loopback detection only.
	localAddr := &net.UDPAddr{IP: nil, Port: Port}
	conn, err := net.ListenUDP("udp4", localAddr)
	if err != nil {
		// Port 6454 may be in use — fall back to ephemeral port
		localAddr.Port = 0
		conn, err = net.ListenUDP("udp4", localAddr)
		if err != nil {
			return nil, fmt.Errorf("bind UDP: %w", err)
		}
		fmt.Printf("Art-Net: port %d in use, bound to %s (ArtPoll replies may not be discoverable)\n", Port, conn.LocalAddr())
	}

	// Determine local IP for ArtPollReply and loopback filtering
	localIP := bindIP
	if localIP == nil {
		localIP = detectLocalIP(destAddr)
	}

	return &Node{
		conn:            conn,
		dest:            destAddr,
		universe:        universe,
		localIP:         localIP,
		ReceivedDmx:     make(chan ReceivedFrame, 8),
		outputUniverses: map[uint16]bool{universe: true},
	}, nil
}

// AddOutputUniverse registers a universe as one we send on (for loopback filtering).
func (n *Node) AddOutputUniverse(universe uint16) {
	n.outputUniverses[universe] = true
}

// SendDmx encodes and transmits an ArtDmx packet for the given frame
// using the node's configured universe.
func (n *Node) SendDmx(frame dmx.Frame) {
	n.SendDmxUniverse(frame, n.universe)
}

// SendDmxUniverse encodes and transmits an ArtDmx packet on a specific universe.
func (n *Node) SendDmxUniverse(frame dmx.Frame, universe uint16) {
	n.seq++
	if n.seq == 0 {
		n.seq = 1
	}

	packet := EncodeArtDmx(frame, n.seq, universe, 0)
	_, err := n.conn.WriteToUDP(packet, n.dest)
	if err != nil {
		fmt.Printf("Art-Net send error: %v\n", err)
	}
}

// Run listens for ArtPoll packets and responds with ArtPollReply.
// It blocks until the context is cancelled.
func (n *Node) Run(ctx context.Context) {
	buf := make([]byte, 1024)

	for {
		if ctx.Err() != nil {
			return
		}

		n.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		nread, addr, err := n.conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}

		data := buf[:nread]

		if n.debug {
			fmt.Printf("[artnet-debug] recv %d bytes from %s\n", nread, addr)
		}

		if IsArtPoll(data) {
			if n.debug {
				fmt.Printf("[artnet-debug] ArtPoll from %s\n", addr)
			}
			reply := EncodeArtPollReply(n.localIP, n.universe, "OpenDmxReciver")
			n.conn.WriteToUDP(reply, addr)
			continue
		}

		if frame, universe, ok := DecodeArtDmx(data); ok {
			// Skip our own output packets (same IP + a universe we send on)
			if addr.IP.Equal(n.localIP) && n.outputUniverses[universe] {
				if n.debug {
					fmt.Printf("[artnet-debug] skipping loopback: uni %d from %s (localIP=%s)\n", universe, addr.IP, n.localIP)
				}
				continue
			}
			if n.debug {
				fmt.Printf("[artnet-debug] ArtDmx: uni %d, %d ch from %s\n", universe, frame.Length, addr.IP)
			}
			select {
			case n.ReceivedDmx <- ReceivedFrame{Frame: frame, Universe: universe, Source: addr.IP}:
			default:
				if n.debug {
					fmt.Printf("[artnet-debug] ReceivedDmx channel full, dropped frame\n")
				}
			}
		} else if n.debug {
			opcode := ""
			if len(data) >= 10 {
				opcode = fmt.Sprintf("0x%02x%02x", data[9], data[8])
			}
			fmt.Printf("[artnet-debug] unknown packet: %d bytes, opcode=%s\n", nread, opcode)
		}
	}
}

// Close shuts down the UDP connection.
func (n *Node) Close() {
	n.conn.Close()
}

// detectLocalIP finds the local IP address that routes toward the given destination.
func detectLocalIP(dest *net.UDPAddr) net.IP {
	conn, err := net.DialUDP("udp4", nil, dest)
	if err != nil {
		return net.IPv4(0, 0, 0, 0)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP
}
