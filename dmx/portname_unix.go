//go:build darwin || linux

package dmx

import (
	"path/filepath"
	"sort"
	"strings"
)

// resolvePortPath accepts either a full device path or a bare device name, so
// both "/dev/cu.usbserial-A1B2" and "cu.usbserial-A1B2" work.
func resolvePortPath(name string) string {
	if strings.HasPrefix(name, "/") {
		return name
	}
	return "/dev/" + name
}

// ListPorts returns the USB serial devices currently present, sorted.
//
// On macOS the device name embeds the adapter's serial number, so it differs
// between machines and between USB ports. Discovery is what makes the tool
// usable without the user first going to look the name up.
func ListPorts() ([]string, error) {
	var found []string
	seen := make(map[string]bool)

	for _, pattern := range portGlobs {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			// Only ErrBadPattern is possible, and the patterns are constants.
			continue
		}
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				found = append(found, m)
			}
		}
	}

	sort.Strings(found)
	return found, nil
}
