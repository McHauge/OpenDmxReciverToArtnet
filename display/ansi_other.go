//go:build !windows

package display

// enableANSI is a no-op outside Windows: macOS, Linux and BSD terminals
// interpret VT escape sequences natively, with nothing to switch on.
func enableANSI() {}
