# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go build -o OpenDmxReciver.exe main.go
./OpenDmxReciver.exe COM3          # positional arg sets COM port
./OpenDmxReciver.exe -channels 128 -artnet -artnet-dest 192.168.1.255 COM3
```

CLI flags override values from `settings.properties`. Run with no args to use file-based config.

No test suite exists yet. No CI/CD pipeline.

## Platform

**Windows-only.** Serial I/O uses kernel32.dll syscalls directly (`dmx/serial.go`). Console output uses Windows ANSI/VT escape sequences (`display/console.go`). This will not compile on Linux/macOS without significant porting.

## Architecture

Go module: `github.com/mc-ha/OpenDmxReciver` — single dependency: `golang.org/x/sys`.

**Data flow:** USB serial (COM port) → DMX receiver → channel to main → display + Art-Net output.

Four packages:

- **`dmx/`** — Reads DMX512 frames from a serial adapter. Two modes: BREAK detection (default, uses overlapped I/O `WaitCommEvent`) and fallback mode (`-no-break-detect`, uses 2ms read timeout gaps). State machine: WaitBreak → WaitStartCode → ReadData. Serial config: 250000 baud, 8N2.
- **`artnet/`** — Encodes/sends ArtDmx packets (OpCode 0x5000) over UDP port 6454. Responds to ArtPoll discovery. Falls back to ephemeral port if 6454 is unavailable.
- **`config/`** — Parses Java-style `settings.properties` (key=value). Generates a template file if missing. CLI flags take precedence over file values.
- **`display/`** — Renders a channel value grid (16 columns) with FPS counter to the Windows console. Quiet mode (`-quiet`) shows status line only.

**Concurrency model:** `main.go` launches goroutines for the DMX receiver, display loop, and Art-Net listener. DMX frames flow through a buffered channel (cap 4). Shutdown is coordinated via `context.Context` cancellation.

## Development Guidelines (from AGENT.md)

- Enter plan mode for non-trivial tasks (3+ steps or architectural decisions).
- Verify changes work before marking complete — run the binary, check behavior.
- Simplicity first; minimal code impact; find root causes, not temporary fixes.
