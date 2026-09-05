# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go build -o OpenDmxReciver .              # .exe on Windows
./OpenDmxReciver                          # auto-detect a single attached adapter
./OpenDmxReciver /dev/cu.usbserial-A1B2   # or COM3 on Windows
./OpenDmxReciver -channels 128 -artnet -artnet-dest 192.168.1.255
```

CLI flags override values from `settings.properties`. Run with no args to use file-based config.

`go test ./...` runs 84 passing tests: `artnet/` (20), `config/` (17), `dmx/` (30), `merge/` (17). `display/` is untested. No CI/CD pipeline.

## Platform

**Windows, macOS and Linux.** Platform-specific code is build-tagged and confined to:

- `dmx/serial_windows.go` — kernel32.dll syscalls, overlapped I/O, `ClearCommError` for break detection.
- `dmx/serial_unix.go` + `dmx/serial_{darwin,linux}.go` — termios. 250000 baud is not a POSIX baud constant, so macOS applies it with `IOSSIOSPEED` (`0x80085402`, an 8-byte `speed_t` — *not* the widely-copied 32-bit `0x80045402`) and Linux with `BOTHER`/`TCSETS2`.
- `dmx/portname_{unix,windows}.go` — device-name resolution and discovery.
- `display/ansi_{windows,other}.go` — enabling VT processing, a no-op off Windows.
- `artnet/sockopt_{unix,windows}.go` — `SO_BROADCAST`.

POSIX has no `WaitCommEvent`, so BREAK arrives in-band via `PARMRK` as `FF 00 00` and is decoded in `dmx/parmrk.go`. `Port.ReadChunk` returns data and the line event together, which is what keeps breaks aligned to the byte offset they occurred at.

## Architecture

Go module: `github.com/mc-ha/OpenDmxReciver` — single dependency: `golang.org/x/sys`.

**Data flow:** USB serial (COM port) → DMX receiver → channel to main → display + merger → Art-Net output.

Five packages:

- **`dmx/`** — Reads DMX512 frames from a serial adapter. `Port.ReadChunk` returns the data that arrived before a line event plus the event itself (`MarkerBreak`/`MarkerError`), so frames are cut at the exact byte offset the break occurred at. Two modes: BREAK detection (default) and fallback (`-no-break-detect`), which anchors on packet length — 512 channel bytes — because USB latency batching destroys the sub-millisecond mark-before-break timing that a gap heuristic would need. State machine: WaitBreak → WaitStartCode → ReadData. Serial config: 250000 baud, 8N2.
- **`artnet/`** — Encodes/sends ArtDmx packets (OpCode 0x5000) over UDP port 6454. Responds to ArtPoll discovery. Falls back to ephemeral port if 6454 is unavailable.
- **`config/`** — Parses Java-style `settings.properties` (key=value). Generates a template file if missing. CLI flags take precedence over file values.
- **`merge/`** — HTP (highest takes precedence) merge of multiple sources per output universe. The local DMX input is source `local`; each `-merge-inputs source:output` pair adds source `artnet:<n>`. Sources silent for `-merge-timeout` seconds are zeroed and the universe recomputed.
- **`display/`** — Renders a channel value grid (16 columns) with FPS counter using VT escape sequences. Quiet mode (`-quiet`) shows status line only.

**Concurrency model:** `main.go` launches goroutines for the DMX receiver, display loop, and Art-Net listener. The receiver itself is a single loop — the old BREAK-listener goroutine is gone. DMX frames flow through a buffered channel (cap 4). Shutdown is coordinated via `context.Context` cancellation.

## Development Guidelines (from AGENT.md)

- Enter plan mode for non-trivial tasks (3+ steps or architectural decisions).
- Verify changes work before marking complete — run the binary, check behavior.
- Simplicity first; minimal code impact; find root causes, not temporary fixes.
