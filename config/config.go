package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	ComPort        string
	Channels       int
	NoBreakDetect  bool
	Quiet          bool
	ArtnetEnabled  bool
	ArtnetDest     string
	ArtnetUniverse int
	ArtnetBind     string
}

func Defaults() Config {
	return Config{
		Channels:       512,
		ArtnetDest:     "255.255.255.255",
		ArtnetUniverse: 0,
	}
}

// LoadProperties reads a Java-style key=value properties file.
// Returns nil, nil if the file does not exist.
func LoadProperties(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	props := make(map[string]string)
	scanner := bufio.NewScanner(f)
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line = strings.TrimPrefix(line, "\xef\xbb\xbf")
			first = false
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		props[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return props, scanner.Err()
}

// Apply sets Config fields from a properties map.
func Apply(props map[string]string, cfg *Config) {
	if v, ok := props["comPort"]; ok {
		cfg.ComPort = v
	}
	if v, ok := props["channels"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Channels = n
		} else {
			fmt.Fprintf(os.Stderr, "Warning: invalid channels value %q, using default\n", v)
		}
	}
	if v, ok := props["noBreakDetect"]; ok {
		cfg.NoBreakDetect = v == "true"
	}
	if v, ok := props["quiet"]; ok {
		cfg.Quiet = v == "true"
	}
	if v, ok := props["artnet"]; ok {
		cfg.ArtnetEnabled = v == "true"
	}
	if v, ok := props["artnetDest"]; ok {
		cfg.ArtnetDest = v
	}
	if v, ok := props["artnetUniverse"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.ArtnetUniverse = n
		} else {
			fmt.Fprintf(os.Stderr, "Warning: invalid artnetUniverse value %q, using default\n", v)
		}
	}
	if v, ok := props["artnetBind"]; ok {
		cfg.ArtnetBind = v
	}
}

const defaultTemplate = `# OpenDmxReciver Settings
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
`

// GenerateDefault writes the default settings.properties template.
func GenerateDefault(path string) error {
	return os.WriteFile(path, []byte(defaultTemplate), 0644)
}

// ExeDir returns the directory containing the running executable.
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}
