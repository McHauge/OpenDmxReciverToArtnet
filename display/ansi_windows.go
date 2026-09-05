//go:build windows

package display

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableANSI turns on VT escape-sequence interpretation for the console.
// Pre-Windows-10 conhost did not process them at all; on modern Windows this
// is what makes the \033[ sequences in console.go render instead of printing
// literally. Failure is not fatal — worst case the output looks noisy.
func enableANSI() {
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err == nil {
		_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}
