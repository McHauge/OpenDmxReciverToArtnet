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
	"github.com/mc-ha/OpenDmxReciver/merge"
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
	quiet := flag.Bool("quiet", cfg.Quiet, "show only receive status and FPS changes instead of full channel grid")
	artnetEnabled := flag.Bool("artnet", cfg.ArtnetEnabled, "enable Art-Net output")
	artnetDest := flag.String("artnet-dest", cfg.ArtnetDest, "Art-Net destination IP (broadcast or unicast)")
	artnetUniverse := flag.Int("artnet-universe", cfg.ArtnetUniverse, "Art-Net universe number (0-32767)")
	artnetBind := flag.String("artnet-bind", cfg.ArtnetBind, "local IP to bind for Art-Net (auto-detect if empty)")
	mergeInputsStr := flag.String("merge-inputs", "", "Art-Net merge inputs as source:output pairs (e.g., 1:0,2:0)")
	mergeTimeout := flag.Int("merge-timeout", cfg.MergeTimeout, "timeout in seconds for Art-Net merge sources (0 = persist forever)")
	debugArtnet := flag.Bool("debug-artnet", false, "enable verbose Art-Net receive logging")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] <COM port>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Open DMX USB Receiver — reads DMX512 data and displays channel values.\n\n")
		fmt.Fprintf(os.Stderr, "Example: %s COM3\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// CLI merge-inputs flag overrides config file
	mergeInputs := cfg.MergeInputs
	if *mergeInputsStr != "" {
		mergeInputs = config.ParseMergeInputs(*mergeInputsStr)
	}

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
	console := display.NewConsole(*channels, *quiet)

	var node *artnet.Node
	if *artnetEnabled {
		node, err = artnet.NewNode(*artnetBind, *artnetDest, uint16(*artnetUniverse))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Art-Net error: %v\n", err)
			os.Exit(1)
		}
		defer node.Close()
		node.SetDebug(*debugArtnet)
		go node.Run(ctx)
		fmt.Printf("Art-Net output enabled: universe %d -> %s\n", *artnetUniverse, *artnetDest)
	}

	// Set up merger
	merger := merge.NewMerger(time.Duration(*mergeTimeout) * time.Second)
	merger.AddMapping("local", uint16(*artnetUniverse))

	// Build set of allowed source universes for filtering
	allowedSources := make(map[uint16]bool)
	for _, m := range mergeInputs {
		srcID := merge.SourceID(fmt.Sprintf("artnet:%d", m.SourceUniverse))
		merger.AddMapping(srcID, uint16(m.OutputUniverse))
		allowedSources[uint16(m.SourceUniverse)] = true
		if node != nil {
			node.AddOutputUniverse(uint16(m.OutputUniverse))
		}
		fmt.Printf(" | Art-Net merge: universe %d -> output universe %d (HTP)\n", m.SourceUniverse, m.OutputUniverse)
	}

	go merger.Run(ctx)

	// Start receiver in background
	go func() {
		if err := receiver.Run(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "\nReceiver error: %v\n", err)
		}
	}()

	// Forward local DMX frames to merger and display
	go func() {
		for {
			select {
			case frame := <-receiver.Frames:
				if console.Quiet() {
					console.RenderStatus(frame)
				} else {
					console.Render(frame)
				}
				merger.Update("local", frame)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Log merge source connect/disconnect events
	go func() {
		for {
			select {
			case ev := <-merger.Events:
				if ev.Connected {
					fmt.Printf(" | Art-Net merge: source %s connected\n", ev.ID)
				} else {
					fmt.Printf(" | Art-Net merge: source %s disconnected (timeout)\n", ev.ID)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Forward received Art-Net frames to merger
	if node != nil && len(mergeInputs) > 0 {
		go func() {
			for {
				select {
				case rf := <-node.ReceivedDmx:
					if !allowedSources[rf.Universe] {
						if *debugArtnet {
							fmt.Printf("[artnet-debug] merge: universe %d not in allowed sources, skipping\n", rf.Universe)
						}
						continue
					}
					srcID := merge.SourceID(fmt.Sprintf("artnet:%d", rf.Universe))
					if *debugArtnet {
						fmt.Printf("[artnet-debug] merge: forwarding uni %d from %s as %s\n", rf.Universe, rf.Source, srcID)
					}
					merger.Update(srcID, rf.Frame)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	console.ShowWaiting()

	// Main output loop
	noDataTimeout := time.NewTimer(10 * time.Second)
	defer noDataTimeout.Stop()

	for {
		select {
		case out := <-merger.Output:
			noDataTimeout.Reset(10 * time.Second)
			if node != nil {
				node.SendDmxUniverse(out.Frame, out.Universe)
			}

		case <-noDataTimeout.C:
			if console.Quiet() {
				console.ShowNotReceiving()
			} else {
				fmt.Println("\n\033[33mWarning: No DMX data received for 10 seconds.\033[0m")
				fmt.Println("Possible causes:")
				fmt.Println("  - No DMX source is transmitting on this universe")
				fmt.Println("  - The adapter may not support receive mode")
				fmt.Println("  - Check wiring and RS-485 termination")
				if !*noBreakDetect {
					fmt.Println("  - Try running with --no-break-detect flag")
				}
				fmt.Println("\nStill listening...")
			}
			noDataTimeout.Reset(30 * time.Second)

		case <-ctx.Done():
			fmt.Println("\n\nShutting down...")
			return
		}
	}
}
