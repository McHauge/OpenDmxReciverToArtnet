package display

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/mc-ha/OpenDmxReciver/dmx"
)

const (
	cols                            = 16
	enableVirtualTerminalProcessing = 0x0004
)

type Console struct {
	maxChannels     int
	quiet           bool
	frameCount      uint64
	lastFPSTime     time.Time
	fps             float64
	fpsFrames       uint64
	lastReportedFPS float64
	firstFrame      bool
}

func NewConsole(maxChannels int, quiet bool) *Console {
	if maxChannels <= 0 || maxChannels > dmx.MaxChannels {
		maxChannels = dmx.MaxChannels
	}
	enableANSI()
	return &Console{
		maxChannels: maxChannels,
		quiet:       quiet,
		lastFPSTime: time.Now(),
		firstFrame:  true,
	}
}

func (c *Console) Render(frame dmx.Frame) {
	c.frameCount++
	c.fpsFrames++

	now := time.Now()
	elapsed := now.Sub(c.lastFPSTime).Seconds()
	if elapsed >= 1.0 {
		c.fps = float64(c.fpsFrames) / elapsed
		c.fpsFrames = 0
		c.lastFPSTime = now
	}

	channelsToShow := frame.Length
	if channelsToShow > c.maxChannels {
		channelsToShow = c.maxChannels
	}

	var sb strings.Builder

	// Cursor home + clear screen
	sb.WriteString("\033[H\033[2J")

	fmt.Fprintf(&sb, "DMX Universe 1 | %d channels | %.1f fps | Frame #%d\n",
		frame.Length, c.fps, c.frameCount)
	sb.WriteString(strings.Repeat("-", 70) + "\n")

	// Column headers
	sb.WriteString("  Ch ")
	for col := 0; col < cols; col++ {
		fmt.Fprintf(&sb, " %03d", col+1)
	}
	sb.WriteString("\n")

	// Channel data rows
	rows := (channelsToShow + cols - 1) / cols
	for row := 0; row < rows; row++ {
		startCh := row * cols
		fmt.Fprintf(&sb, " %03d:", startCh+1)
		for col := 0; col < cols; col++ {
			ch := startCh + col
			if ch < channelsToShow {
				val := frame.Channels[ch]
				if val > 0 {
					fmt.Fprintf(&sb, " \033[33m%3d\033[0m", val)
				} else {
					fmt.Fprintf(&sb, " %3d", val)
				}
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\nPress Ctrl+C to exit\n")

	os.Stdout.WriteString(sb.String())
}

func (c *Console) Quiet() bool {
	return c.quiet
}

func (c *Console) RenderStatus(frame dmx.Frame) {
	c.frameCount++
	c.fpsFrames++

	now := time.Now()
	elapsed := now.Sub(c.lastFPSTime).Seconds()
	if elapsed >= 1.0 {
		c.fps = float64(c.fpsFrames) / elapsed
		c.fpsFrames = 0
		c.lastFPSTime = now
	}

	fpsDiff := c.fps - c.lastReportedFPS
	if fpsDiff < 0 {
		fpsDiff = -fpsDiff
	}

	if c.firstFrame || fpsDiff >= 1.0 {
		c.firstFrame = false
		c.lastReportedFPS = c.fps
		fmt.Printf("\r\033[K\033[32mDMX Receiving\033[0m | %.1f fps | %d channels", c.fps, frame.Length)
	}
}

func (c *Console) ShowNotReceiving() {
	fmt.Printf("\r\033[K\033[33mDMX Not Receiving\033[0m | No data for 10s — check source/wiring")
}

func (c *Console) ShowWaiting() {
	fmt.Print("\033[H\033[2J")
	fmt.Println("DMX Universe 1 | Waiting for data...")
	fmt.Println("Listening for DMX BREAK signal on serial port...")
	fmt.Println("\nPress Ctrl+C to exit")
}

func enableANSI() {
	var mode uint32
	handle := windows.Handle(os.Stdout.Fd())
	if err := windows.GetConsoleMode(handle, &mode); err == nil {
		_ = setConsoleMode(handle, mode|enableVirtualTerminalProcessing)
	}
}

var procSetConsoleMode = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleMode")

func setConsoleMode(handle windows.Handle, mode uint32) error {
	r, _, err := procSetConsoleMode.Call(uintptr(handle), uintptr(mode))
	_ = unsafe.Sizeof(r) // suppress unused
	if r == 0 {
		return err
	}
	return nil
}
