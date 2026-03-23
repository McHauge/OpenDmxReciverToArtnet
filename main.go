package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/mc-ha/OpenDmxReciver/artnet"
	"github.com/mc-ha/OpenDmxReciver/config"
	"github.com/mc-ha/OpenDmxReciver/display"
	"github.com/mc-ha/OpenDmxReciver/dmx"
)

func main() {
	// Load settings.properties from the executable's directory
	exeDir := config.ExeDir()
	propsPath := filepath.Join(exeDir, "settings.properties")

	cfg := config.Defaults()
	props, err := config.LoadProperties(propsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: error reading %s: %v\n", propsPath, err)
	} else if props == nil {
		if err := config.GenerateDefault(propsPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not create %s: %v\n", propsPath, err)
		} else {
			fmt.Printf("Generated default settings: %s\n", propsPath)
		}
	} else {
		config.Apply(props, &cfg)
	}

	channels := flag.Int("channels", cfg.Channels, "number of DMX channels to display (1-512)")
	noBreakDetect := flag.Bool("no-break-detect", cfg.NoBreakDetect, "fallback mode: use read timeouts instead of BREAK detection")
	artnetEnabled := flag.Bool("artnet", cfg.ArtnetEnabled, "enable Art-Net output")
	artnetDest := flag.String("artnet-dest", cfg.ArtnetDest, "Art-Net destination IP (broadcast or unicast)")
	artnetUniverse := flag.Int("artnet-universe", cfg.ArtnetUniverse, "Art-Net universe number (0-32767)")
	artnetBind := flag.String("artnet-bind", cfg.ArtnetBind, "local IP to bind for Art-Net (auto-detect if empty)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] <COM port>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Open DMX USB Receiver — reads DMX512 data and displays channel values.\n\n")
		fmt.Fprintf(os.Stderr, "Example: %s COM3\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	portName := flag.Arg(0)
	if portName == "" {
		portName = cfg.ComPort
	}
	if portName == "" {
		flag.Usage()
		os.Exit(1)
	}

	fmt.Printf("Opening %s at 250000 baud (8N2)...\n", portName)

	port, err := dmx.OpenSerialPort(portName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nTroubleshooting:\n")
		fmt.Fprintf(os.Stderr, "  - Is the Open DMX USB adapter plugged in?\n")
		fmt.Fprintf(os.Stderr, "  - Is %s the correct COM port? (Check Device Manager)\n", portName)
		fmt.Fprintf(os.Stderr, "  - Is another application using the port?\n")
		os.Exit(1)
	}
	defer port.Close()

	fmt.Println("Port opened successfully.")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	receiver := dmx.NewReceiver(port, *noBreakDetect)
	console := display.NewConsole(*channels)

	var node *artnet.Node
	if *artnetEnabled {
		node, err = artnet.NewNode(*artnetBind, *artnetDest, uint16(*artnetUniverse))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Art-Net error: %v\n", err)
			os.Exit(1)
		}
		defer node.Close()
		go node.Run(ctx)
		fmt.Printf("Art-Net output enabled: universe %d -> %s\n", *artnetUniverse, *artnetDest)
	}

	// Start receiver in background
	go func() {
		if err := receiver.Run(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "\nReceiver error: %v\n", err)
		}
	}()

	console.ShowWaiting()

	// Display loop
	noDataTimeout := time.NewTimer(10 * time.Second)
	defer noDataTimeout.Stop()

	for {
		select {
		case frame := <-receiver.Frames:
			noDataTimeout.Reset(10 * time.Second)
			console.Render(frame)
			if node != nil {
				node.SendDmx(frame)
			}

		case <-noDataTimeout.C:
			fmt.Println("\n\033[33mWarning: No DMX data received for 10 seconds.\033[0m")
			fmt.Println("Possible causes:")
			fmt.Println("  - No DMX source is transmitting on this universe")
			fmt.Println("  - The adapter may not support receive mode")
			fmt.Println("  - Check wiring and RS-485 termination")
			if !*noBreakDetect {
				fmt.Println("  - Try running with --no-break-detect flag")
			}
			fmt.Println("\nStill listening...")
			noDataTimeout.Reset(30 * time.Second)

		case <-ctx.Done():
			fmt.Println("\n\nShutting down...")
			return
		}
	}
}
