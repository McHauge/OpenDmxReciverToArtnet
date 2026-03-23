package artnet

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/mc-ha/OpenDmxReciver/dmx"
)

// Node sends ArtDmx packets and responds to ArtPoll discovery.
type Node struct {
	conn     *net.UDPConn
	dest     *net.UDPAddr
	universe uint16
	seq      byte
	localIP  net.IP
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

	localAddr := &net.UDPAddr{IP: bindIP, Port: Port}
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

	// Detect local IP if not specified
	localIP := bindIP
	if localIP == nil {
		localIP = detectLocalIP(destAddr)
	}

	return &Node{
		conn:     conn,
		dest:     destAddr,
		universe: universe,
		localIP:  localIP,
	}, nil
}

// SendDmx encodes and transmits an ArtDmx packet for the given frame.
func (n *Node) SendDmx(frame dmx.Frame) {
	n.seq++
	if n.seq == 0 {
		n.seq = 1
	}

	packet := EncodeArtDmx(frame, n.seq, n.universe, 0)
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

		if IsArtPoll(buf[:nread]) {
			reply := EncodeArtPollReply(n.localIP, n.universe, "OpenDmxReciver")
			n.conn.WriteToUDP(reply, addr)
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
