//go:build windows

package dmx

import (
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// resolvePortPath prefixes a bare COM name with the \\.\ device namespace,
// which is required for COM10 and above.
func resolvePortPath(name string) string {
	if strings.HasPrefix(name, `\\.\`) {
		return name
	}
	return `\\.\` + name
}

// ListPorts returns the serial ports the system knows about, sorted.
func ListPorts() ([]string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.READ)
	if err != nil {
		// The key is absent when no serial hardware has ever been attached.
		return nil, nil
	}
	defer k.Close()

	names, err := k.ReadValueNames(0)
	if err != nil {
		return nil, err
	}

	var found []string
	for _, n := range names {
		if v, _, err := k.GetStringValue(n); err == nil && v != "" {
			found = append(found, v)
		}
	}

	sort.Strings(found)
	return found, nil
}

// PortExample is a representative port name for help text.
func PortExample() string { return "COM3" }

// PortHint tells the user how to find their adapter.
func PortHint() string { return "check Device Manager under Ports (COM & LPT)" }

// Windows reports BREAK directly through ClearCommError's CE_BREAK bit, so
// there is no need to infer boundaries from read sizes.
const (
	BreakDetectSupported = true
	breakFromShortRead   = false
)
