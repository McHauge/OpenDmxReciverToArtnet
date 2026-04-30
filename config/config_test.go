package config

import (
	"os"
	"path/filepath"
	"testing"
)

// --- ParseMergeInputs tests ---

func TestParseMergeInputsValid(t *testing.T) {
	mappings := ParseMergeInputs("1:0, 2:0, 3:1")

	if len(mappings) != 3 {
		t.Fatalf("expected 3 mappings, got %d", len(mappings))
	}

	expected := []MergeMapping{
		{SourceUniverse: 1, OutputUniverse: 0},
		{SourceUniverse: 2, OutputUniverse: 0},
		{SourceUniverse: 3, OutputUniverse: 1},
	}

	for i, m := range mappings {
		if m != expected[i] {
			t.Errorf("mapping %d: expected %+v, got %+v", i, expected[i], m)
		}
	}
}

func TestParseMergeInputsSingle(t *testing.T) {
	mappings := ParseMergeInputs("5:3")

	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}
	if mappings[0].SourceUniverse != 5 || mappings[0].OutputUniverse != 3 {
		t.Errorf("expected {5:3}, got %+v", mappings[0])
	}
}

func TestParseMergeInputsEmpty(t *testing.T) {
	mappings := ParseMergeInputs("")
	if mappings != nil {
		t.Errorf("expected nil for empty string, got %+v", mappings)
	}
}

func TestParseMergeInputsWhitespace(t *testing.T) {
	mappings := ParseMergeInputs("   ")
	if mappings != nil {
		t.Errorf("expected nil for whitespace-only string, got %+v", mappings)
	}
}

func TestParseMergeInputsMissingSeparator(t *testing.T) {
	mappings := ParseMergeInputs("1, 2:3")
	// "1" is invalid (no separator), so only one valid mapping expected
	if len(mappings) != 1 {
		t.Fatalf("expected 1 valid mapping (one invalid skipped), got %d", len(mappings))
	}
	if mappings[0].SourceUniverse != 2 || mappings[0].OutputUniverse != 3 {
		t.Errorf("unexpected mapping: %+v", mappings[0])
	}
}

func TestParseMergeInputsNonNumeric(t *testing.T) {
	mappings := ParseMergeInputs("a:b, 1:2")
	if len(mappings) != 1 {
		t.Fatalf("expected 1 valid mapping (one invalid skipped), got %d", len(mappings))
	}
	if mappings[0].SourceUniverse != 1 || mappings[0].OutputUniverse != 2 {
		t.Errorf("unexpected mapping: %+v", mappings[0])
	}
}

func TestParseMergeInputsWithExtraSpaces(t *testing.T) {
	mappings := ParseMergeInputs("  10 : 20  ,  30 : 40  ")

	if len(mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(mappings))
	}
	if mappings[0].SourceUniverse != 10 || mappings[0].OutputUniverse != 20 {
		t.Errorf("mapping 0: expected {10:20}, got %+v", mappings[0])
	}
	if mappings[1].SourceUniverse != 30 || mappings[1].OutputUniverse != 40 {
		t.Errorf("mapping 1: expected {30:40}, got %+v", mappings[1])
	}
}

// --- Apply tests ---

func TestApplyAllFields(t *testing.T) {
	props := map[string]string{
		"comPort":        "COM5",
		"channels":       "256",
		"noBreakDetect":  "true",
		"quiet":          "true",
		"artnet":         "true",
		"artnetDest":     "192.168.1.100",
		"artnetUniverse": "42",
		"artnetBind":     "192.168.1.10",
		"mergeInputs":    "1:0,2:0",
		"mergeTimeout":   "10",
	}

	cfg := Defaults()
	Apply(props, &cfg)

	if cfg.ComPort != "COM5" {
		t.Errorf("expected ComPort COM5, got %s", cfg.ComPort)
	}
	if cfg.Channels != 256 {
		t.Errorf("expected Channels 256, got %d", cfg.Channels)
	}
	if !cfg.NoBreakDetect {
		t.Error("expected NoBreakDetect true")
	}
	if !cfg.Quiet {
		t.Error("expected Quiet true")
	}
	if !cfg.ArtnetEnabled {
		t.Error("expected ArtnetEnabled true")
	}
	if cfg.ArtnetDest != "192.168.1.100" {
		t.Errorf("expected ArtnetDest 192.168.1.100, got %s", cfg.ArtnetDest)
	}
	if cfg.ArtnetUniverse != 42 {
		t.Errorf("expected ArtnetUniverse 42, got %d", cfg.ArtnetUniverse)
	}
	if cfg.ArtnetBind != "192.168.1.10" {
		t.Errorf("expected ArtnetBind 192.168.1.10, got %s", cfg.ArtnetBind)
	}
	if len(cfg.MergeInputs) != 2 {
		t.Fatalf("expected 2 MergeInputs, got %d", len(cfg.MergeInputs))
	}
	if cfg.MergeTimeout != 10 {
		t.Errorf("expected MergeTimeout 10, got %d", cfg.MergeTimeout)
	}
}

func TestApplyPartialLeavesDefaults(t *testing.T) {
	props := map[string]string{
		"comPort": "COM3",
	}

	cfg := Defaults()
	Apply(props, &cfg)

	if cfg.ComPort != "COM3" {
		t.Errorf("expected ComPort COM3, got %s", cfg.ComPort)
	}
	// Defaults should remain
	if cfg.Channels != 512 {
		t.Errorf("expected default Channels 512, got %d", cfg.Channels)
	}
	if cfg.ArtnetDest != "255.255.255.255" {
		t.Errorf("expected default ArtnetDest, got %s", cfg.ArtnetDest)
	}
	if cfg.MergeTimeout != 5 {
		t.Errorf("expected default MergeTimeout 5, got %d", cfg.MergeTimeout)
	}
}

func TestApplyInvalidChannelsKeepsDefault(t *testing.T) {
	props := map[string]string{
		"channels": "abc",
	}

	cfg := Defaults()
	Apply(props, &cfg)

	if cfg.Channels != 512 {
		t.Errorf("expected default Channels 512 after invalid value, got %d", cfg.Channels)
	}
}

func TestApplyInvalidUniverseKeepsDefault(t *testing.T) {
	props := map[string]string{
		"artnetUniverse": "xyz",
	}

	cfg := Defaults()
	Apply(props, &cfg)

	if cfg.ArtnetUniverse != 0 {
		t.Errorf("expected default ArtnetUniverse 0 after invalid value, got %d", cfg.ArtnetUniverse)
	}
}

func TestApplyBoolFalse(t *testing.T) {
	props := map[string]string{
		"noBreakDetect": "false",
		"quiet":         "false",
		"artnet":        "false",
	}

	cfg := Config{}
	Apply(props, &cfg)

	if cfg.NoBreakDetect {
		t.Error("expected NoBreakDetect false")
	}
	if cfg.Quiet {
		t.Error("expected Quiet false")
	}
	if cfg.ArtnetEnabled {
		t.Error("expected ArtnetEnabled false")
	}
}

// --- LoadProperties tests ---

func TestLoadPropertiesValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.properties")
	content := []byte("key1=value1\nkey2=value2\n# comment\n\nkey3 = value3 ")
	os.WriteFile(path, content, 0644)

	props, err := LoadProperties(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if props["key1"] != "value1" {
		t.Errorf("expected key1=value1, got %q", props["key1"])
	}
	if props["key2"] != "value2" {
		t.Errorf("expected key2=value2, got %q", props["key2"])
	}
	if props["key3"] != "value3" {
		t.Errorf("expected key3=value3, got %q", props["key3"])
	}
	if len(props) != 3 {
		t.Errorf("expected 3 properties, got %d", len(props))
	}
}

func TestLoadPropertiesMissingFile(t *testing.T) {
	props, err := LoadProperties("/nonexistent/path/file.properties")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if props != nil {
		t.Errorf("expected nil props for missing file, got %+v", props)
	}
}

func TestLoadPropertiesWithBOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.properties")
	// UTF-8 BOM followed by a property
	content := []byte("\xef\xbb\xbfkey=value")
	os.WriteFile(path, content, 0644)

	props, err := LoadProperties(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Key should not have BOM prefix
	if val, ok := props["key"]; !ok || val != "value" {
		t.Errorf("expected key=value without BOM, got key=%q val=%q", "key", val)
	}
}

func TestLoadPropertiesNoEqualsLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.properties")
	content := []byte("valid=prop\nno-equals-here\nalso-valid=123")
	os.WriteFile(path, content, 0644)

	props, err := LoadProperties(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(props) != 2 {
		t.Errorf("expected 2 properties (line without = skipped), got %d", len(props))
	}
}

// --- Defaults test ---

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	if cfg.Channels != 512 {
		t.Errorf("expected default Channels 512, got %d", cfg.Channels)
	}
	if cfg.ArtnetDest != "255.255.255.255" {
		t.Errorf("expected default ArtnetDest, got %s", cfg.ArtnetDest)
	}
	if cfg.ArtnetUniverse != 0 {
		t.Errorf("expected default ArtnetUniverse 0, got %d", cfg.ArtnetUniverse)
	}
	if cfg.MergeTimeout != 5 {
		t.Errorf("expected default MergeTimeout 5, got %d", cfg.MergeTimeout)
	}
}