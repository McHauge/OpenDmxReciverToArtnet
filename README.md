# OpenDmxReciverToArtnet

A small cross-platform console tool that **receives** DMX512 from a USB→DMX adapter, shows a live channel grid in the terminal, and can rebroadcast the universe as **Art-Net** — optionally HTP-merging other incoming Art-Net universes into the output.

Written in Go with a single dependency (`golang.org/x/sys`). Go module path: `github.com/mc-ha/OpenDmxReciver`.

```
Opening COM3 at 250000 baud (8N2)...
Port opened successfully.
Art-Net output enabled: universe 0 -> 192.168.1.255

DMX Universe 1 | 512 channels | 43.8 fps | Frame #1247
----------------------------------------------------------------------
       001 002 003 004 005 006 007 008 009 010 011 012 013 014 015 016
001:   255 128 000 000 064 000 000 000 000 000 000 000 000 000 000 000
017:   000 000 000 000 000 000 000 000 000 000 000 000 000 000 000 000
...
```

---

## Hardware

Developed and tested against this adapter:

**[DSD TECH SH-RS09B USB to DMX cable](https://www.amazon.de/dp/B07WV6P5W6)** — Amazon.de, ASIN `B07WV6P5W6`

> **Important:** this is a *receive* application. Many cheap FTDI-based "Open DMX USB" cables are **transmit-only** and will never deliver a single byte to this tool no matter how the wiring is done. The unit linked above works for RX.

The port is opened at **250000 baud, 8 data bits, no parity, 2 stop bits (8N2)** — the DMX512 line settings.

| OS | Device | How to find it |
|---|---|---|
| Windows | `\\.\COM3` | Device Manager → Ports (COM & LPT) |
| macOS | `/dev/cu.usbserial-XXXX` | `ls /dev/cu.*` |
| Linux | `/dev/ttyUSB0` | `ls /dev/ttyUSB* /dev/ttyACM*` (you may need the `dialout` group) |

With no port argument the tool auto-detects a single attached adapter, which is
the easy path on macOS where the device name embeds the adapter's serial number.

### BREAK detection per platform

DMX packets are delimited by a BREAK. Windows reports it via `ClearCommError`,
and Linux's `ftdi_sio` driver reports it to the tty layer where `PARMRK` escapes
it into the read stream as `FF 00 00`.

**macOS reports no break, but the signal survives anyway.** Apple's
`AppleUSBFTDI` never passes BREAK to the tty layer — verified against an
SH-RS09B on macOS 15 with a live 44fps source: the termios flags read back
correctly and data arrives at the right rate, yet zero `FF 00 00` sequences
appear where ~88 per two seconds should, and setting `IGNBRK` changes nothing.

What does survive is the FTDI chip's own behaviour. The UART turns the break
into a stray `0x00` data byte, and the line idling through the break makes the
chip flush a partial USB packet. So a read shorter than the 62-byte payload
marks a frame end, giving a 514-byte cadence (513 for the packet, one for the
stray byte).

That inference is noisy — the tty layer delivers data incrementally, so roughly
one read in six ends short for unrelated reasons. The receiver therefore learns
the source's packet length by majority vote over the first 24 breaks, then
frames on length and uses breaks only to recover phase. Measured result: 412
consecutive frames, all exactly 512 channels, at 43.8fps against a theoretical
44.1.

## Requirements

- **Windows, macOS and Linux.** Serial I/O is platform-split: `kernel32.dll` syscalls on Windows ([dmx/serial_windows.go](dmx/serial_windows.go)), termios on POSIX ([dmx/serial_unix.go](dmx/serial_unix.go)) with the non-standard 250000 baud rate applied via `IOSSIOSPEED` on macOS and `BOTHER`/`TCSETS2` on Linux.
- Go **1.26.1** or newer to build.
- Dependency: `golang.org/x/sys v0.42.0` (only).

## Build

```bash
go build -o OpenDmxReciver .          # macOS / Linux
go build -o OpenDmxReciver.exe .      # Windows
```

## Quick start

```bash
# Full channel grid on COM3
OpenDmxReciver.exe COM3          # Windows
./OpenDmxReciver                 # macOS/Linux, auto-detect the adapter

# Just a status + FPS line
./OpenDmxReciver -quiet /dev/cu.usbserial-A1B2C3

# Forward the incoming DMX to Art-Net universe 0 on the local broadcast address
./OpenDmxReciver -artnet -artnet-dest 192.168.1.255 -artnet-universe 0

# Also merge incoming Art-Net universes 1 and 2 into output universe 0 (HTP)
./OpenDmxReciver -artnet -merge-inputs 1:0,2:0
```

## How it works

```mermaid
flowchart TD
    ADAPTER[USB DMX adapter<br/>250000 baud, 8N2] --> RX
    RX[dmx.Receiver<br/>BREAK state machine] -->|frames chan, cap 4| MAIN(main loop)

    MAIN --> DISP[display.Console<br/>channel grid / quiet line]
    MAIN -->|as source local| MERGE

    NET([Art-Net network]) -->|ArtDmx in| IN[artnet.Node listener<br/>UDP 6454]
    IN -->|as source artnet N| MERGE[merge.Merger<br/>HTP per channel<br/>+ source timeout]

    MERGE -->|merged universe| OUT[artnet.Node sender<br/>ArtDmx, OpCode 0x5000]
    OUT --> NET

    IN -.->|ArtPoll| REPLY[ArtPollReply] -.-> NET
```

The DMX receiver, the display loop and the Art-Net listener each run as goroutines; frames flow through a buffered channel (capacity 4, non-blocking send — frames are dropped rather than stalling the reader). Shutdown is coordinated with `context.Context` cancellation on Ctrl+C.

## CLI flags

```
Usage: OpenDmxReciver [flags] <serial port>
```

| Flag | Default | Description |
| --- | --- | --- |
| `-channels` | `512` | number of DMX channels to display (1-512) |
| `-no-break-detect` | `false` | fallback mode: use read timeouts instead of BREAK detection |
| `-quiet` | `false` | show only receive status and FPS changes instead of full channel grid |
| `-artnet` | `false` | enable Art-Net output |
| `-artnet-dest` | `255.255.255.255` | Art-Net destination IP (broadcast or unicast) |
| `-artnet-universe` | `0` | Art-Net universe number (0-32767) |
| `-artnet-bind` | *(auto-detect)* | local IP to bind for Art-Net |
| `-merge-inputs` | *(empty)* | Art-Net merge inputs as `source:output` pairs (e.g. `1:0,2:0`) |
| `-merge-timeout` | `5` | timeout in seconds for Art-Net merge sources (0 = persist forever) |
| `-debug-artnet` | `false` | enable verbose Art-Net receive logging |

The COM port is the first positional argument. If omitted, `comPort` from `settings.properties` is used; if that is empty too, usage is printed and the program exits.

CLI flags override the values from `settings.properties`. The one exception is `-merge-inputs`, which only overrides the file when it is non-empty ([main.go:58-61](main.go#L58-L61)) — so a config-file merge setup stays active unless you explicitly pass the flag.

## Configuration — `settings.properties`

On first run, if no `settings.properties` exists **next to the executable** (not the current working directory), a commented template is generated there and the path is printed. The file is Java-style `key=value`, `#` starts a comment, blank lines are ignored. It is gitignored.

```properties
# OpenDmxReciver Settings
# Lines starting with # are comments. Blank lines are ignored.
# CLI flags override values set here.

# COM port for the Open DMX USB adapter (e.g., COM3)
comPort=

# Number of DMX channels to display (1-512)
channels=512

# Fallback mode: use read timeouts instead of BREAK detection (true/false)
noBreakDetect=false

# Quiet mode: show only receive status and FPS changes instead of full channel grid (true/false)
quiet=false

# Enable Art-Net output (true/false)
artnet=false

# Art-Net destination IP (broadcast or unicast)
artnetDest=255.255.255.255

# Art-Net universe number (0-32767)
artnetUniverse=0

# Local IP to bind for Art-Net (leave empty for auto-detect)
artnetBind=

# Merge Art-Net inputs: sourceUniverse:outputUniverse (comma-separated)
# Received Art-Net data is HTP-merged per output universe.
# Example: mergeInputs=1:0,2:0  (merge universes 1 and 2 into output 0)
mergeInputs=

# Timeout in seconds for Art-Net merge sources (0 = persist forever)
mergeTimeout=5
```

Booleans must be the literal string `true` to be enabled. An unparsable integer prints a warning and falls back to the default.

## Art-Net

- UDP port **6454**, ArtDmx `OpCode 0x5000`, protocol version 14.
- The node always binds `0.0.0.0:6454` so that broadcast packets are received. If the port is already in use it retries on an ephemeral port and warns — in that case ArtPoll discovery will not work, but output still does.
- `-artnet-bind` does **not** change the bind address; it sets the local IP reported in the ArtPollReply and used for loopback filtering. Left empty, the local IP is auto-detected by probing a route toward the destination.
- **ArtPoll** requests are answered with an ArtPollReply (unicast back to the sender) advertising one input port, short name `OpenDmxReciver`, long name `OpenDmxReciver Art-Net Node`.
- The universe number is the flat 15-bit Port-Address (0–32767); it is split into the SubUni and Net bytes on the wire, so there are no separate net/subnet options.
- Every ArtDmx packet is a fixed 530 bytes — an 18-byte header plus a full 512 channels, zero-padded — regardless of how many channels the source actually sent.
- The sequence field increments per packet and wraps `0 → 1` (0 means "sequencing disabled" in the spec).

## Merging other Art-Net universes

`-merge-inputs 1:0,2:0` (or `mergeInputs=1:0,2:0` in the config) combines other Art-Net sources with the locally received DMX:

- The DMX coming in over USB is registered as source `local` on the universe given by `-artnet-universe`.
- Each `source:output` pair registers an Art-Net source `artnet:<source>` feeding output universe `<output>`.
- All sources mapped to the same output universe are merged **HTP — highest takes precedence, per channel**. The merged universe is then sent out as ArtDmx.
- Incoming ArtDmx on a universe that is *not* listed as a source is discarded, and the tool's own output universes are filtered out by source IP so it never merges with itself.
- A source that stops sending for `-merge-timeout` seconds is considered gone: its contribution is zeroed, the universe is recomputed, and a `disconnected (timeout)` line is printed. `-merge-timeout 0` keeps sources forever.

Connect/disconnect events are printed as they happen:

```
 | Art-Net merge: universe 1 -> output universe 0 (HTP)
 | Art-Net merge: source artnet:1 connected
 | Art-Net merge: source artnet:1 disconnected (timeout)
```

## Display modes

**Default (grid):** the screen is redrawn each frame with a header (`channels | fps | frame #`), a 16-column value grid with `001:`-style row labels, and non-zero values highlighted in yellow. `-channels` limits how many channels are drawn.

**Quiet (`-quiet`):** a single self-rewriting status line, only re-printed when the FPS changes by 1 or more:

```
DMX Receiving | 44.0 fps | 512 channels
```

**No-data watchdog:** if no frame arrives, a warning is shown after 10 seconds and then every 30 seconds after that.

## Frame detection modes

**BREAK detection (default)** uses overlapped I/O and `WaitCommEvent` to catch the DMX BREAK, then validates the `0x00` start code and reads the channel data. This is the accurate mode and requires an adapter that reports break events.

**Fallback (`-no-break-detect`)** has no break events available and instead treats read-timeout gaps (>1 ms with no bytes) as frame boundaries. Try this if the adapter delivers data but no frames are ever assembled.

## Troubleshooting

**Port will not open** — check that the adapter is plugged in, that the COM port number matches Device Manager, and that no other application (lighting software, a terminal) is holding the port open. The port is opened without sharing.

**Port opens but no data after 10 seconds** — the most likely cause is a transmit-only adapter (see [Hardware](#hardware)). Otherwise check that something is actually sending DMX, check the XLR wiring, and try `-no-break-detect`.

**`Art-Net: port 6454 in use`** — another Art-Net application (or a second instance of this tool) already owns the port. Output still works from the ephemeral port it fell back to, but the node will not answer ArtPoll discovery.

**Merged universes never appear** — the source universe must be listed in `-merge-inputs`; anything else is dropped on receive. Use `-debug-artnet` to see what is actually arriving.

## Tests

```bash
go test ./...
```

95 tests cover `artnet/` (20), `config/` (17), `dmx/` (41) and `merge/` (17). The `dmx/` tests exercise the PARMRK break decoder and the receiver state machine through a fake port, so they run on every platform. `display/` is not covered.


## License

MIT — see [LICENSE](LICENSE). Copyright © 2026 Andreas Thomsen.
