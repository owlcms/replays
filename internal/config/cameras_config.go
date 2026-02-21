package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/owlcms/replays/internal/logging"
)

//go:embed cameras_config.toml
var defaultSharedCamerasConfig []byte

//go:embed cameras.toml
var defaultInstanceCamerasConfig []byte

var camerasConfigSourcePath string

// CamerasConfig is the top-level configuration for the cameras program.
type CamerasConfig struct {
	Multicast MulticastConfig  `toml:"multicast"`
	Cameras   CamerasSelection `toml:"cameras"`
	Software  SoftwareEncoder  `toml:"software"`
	Encoders  []EncoderConfig  `toml:"encoder"`
	Output    OutputConfig     `toml:"output"`
}

// MulticastConfig holds multicast streaming settings.
type MulticastConfig struct {
	IP        string `toml:"ip"`
	StartPort int    `toml:"startPort"`
	PktSize   int    `toml:"pktSize"`
}

// CamerasSelection holds camera filtering and mode selection settings.
type CamerasSelection struct {
	IncludeAll     bool     `toml:"includeAll"`
	FormatPriority []string `toml:"formatPriority"`
	ModePriority   []string `toml:"modePriority"`
}

// SoftwareEncoder holds the software (libx264) fallback parameters.
type SoftwareEncoder struct {
	OutputParameters string `toml:"outputParameters"`
}

// EncoderConfig defines one hardware encoder and its ffmpeg parameters.
type EncoderConfig struct {
	Name             string `toml:"name"`
	Description      string `toml:"description"`
	InputParameters  string `toml:"inputParameters"`
	OutputParameters string `toml:"outputParameters"`
	TestInit         string `toml:"testInit"`
	Platform         string `toml:"platform"` // "linux", "windows", or "" (any)
}

// OutputConfig holds common output flags.
type OutputConfig struct {
	GopMultiplier int    `toml:"gopMultiplier"`
	ExtraFlags    string `toml:"extraFlags"`
}

// ModePriorityEntry is a parsed version of a "WxH@FPS" string from the config.
type ModePriorityEntry struct {
	Width  int
	Height int
	MinFps int
}

// LoadCamerasConfig loads the cameras configuration by merging:
// 1) Shared settings (cameras_config.toml / camera_configs.toml), then
// 2) Instance settings (cameras.toml)
//
// This allows shared encoder/OS behavior across applications while keeping
// per-instance runtime settings (like multicast start port) separate.
func LoadCamerasConfig() (*CamerasConfig, error) {
	var cfg CamerasConfig

	baseDirs := []string{}
	if exe, err := os.Executable(); err == nil {
		baseDirs = append(baseDirs, filepath.Dir(exe))
	}
	if cwd, err := os.Getwd(); err == nil {
		baseDirs = append(baseDirs, cwd)
	}
	baseDirs = append(baseDirs, GetInstallDir())

	sharedFilenames := []string{"cameras_config.toml", "camera_configs.toml"}
	instanceFilename := "cameras.toml"

	sharedLoaded := false
	for _, dir := range baseDirs {
		for _, name := range sharedFilenames {
			path := filepath.Join(dir, name)
			if _, err := os.Stat(path); err == nil {
				logging.InfoLogger.Printf("Loading shared cameras config from %s", path)
				if _, err := toml.DecodeFile(path, &cfg); err != nil {
					return nil, fmt.Errorf("failed to parse %s: %w", path, err)
				}
				sharedLoaded = true
				break
			}
		}
		if sharedLoaded {
			break
		}
	}

	if !sharedLoaded {
		logging.InfoLogger.Println("No cameras_config.toml found, using embedded shared defaults")
		if _, err := toml.Decode(string(defaultSharedCamerasConfig), &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse embedded cameras_config.toml: %w", err)
		}
	}

	camerasConfigSourcePath = ""
	for _, dir := range baseDirs {
		path := filepath.Join(dir, instanceFilename)
		if _, err := os.Stat(path); err == nil {
			logging.InfoLogger.Printf("Loading cameras instance config from %s", path)
			if _, err := toml.DecodeFile(path, &cfg); err != nil {
				return nil, fmt.Errorf("failed to parse %s: %w", path, err)
			}
			camerasConfigSourcePath = path
			break
		}
	}

	if camerasConfigSourcePath == "" {
		logging.InfoLogger.Println("No cameras.toml found, using embedded instance defaults")
		if _, err := toml.Decode(string(defaultInstanceCamerasConfig), &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse embedded cameras.toml: %w", err)
		}
	}

	cfg.filterEncodersForPlatform()
	cfg.applyDefaults()
	return &cfg, nil
}

// GetCamerasConfigSourcePath returns the file path used by LoadCamerasConfig.
// Empty string means defaults were loaded from embedded config.
func GetCamerasConfigSourcePath() string {
	return camerasConfigSourcePath
}

// SaveCamerasStartPort updates multicast.startPort in the loaded cameras.toml file.
func SaveCamerasStartPort(startPort int) error {
	if startPort < 1 || startPort > 65535 {
		return fmt.Errorf("invalid startPort %d", startPort)
	}

	configPath := GetCamerasConfigSourcePath()
	if configPath == "" {
		return fmt.Errorf("cameras config loaded from embedded defaults")
	}

	input, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read cameras config: %w", err)
	}

	lines := strings.Split(string(input), "\n")
	multicastStart := -1
	multicastEnd := len(lines)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[multicast]" {
			multicastStart = i
			for j := i + 1; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
					multicastEnd = j
					break
				}
			}
			break
		}
	}

	startPortLine := fmt.Sprintf("    startPort = %d", startPort)

	if multicastStart == -1 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines,
			"[multicast]",
			startPortLine,
		)
	} else {
		updated := false
		for i := multicastStart + 1; i < multicastEnd; i++ {
			trimmed := strings.TrimSpace(lines[i])
			if strings.HasPrefix(trimmed, "startPort") {
				indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
				lines[i] = fmt.Sprintf("%sstartPort = %d", indent, startPort)
				updated = true
				break
			}
		}
		if !updated {
			newLines := make([]string, 0, len(lines)+1)
			newLines = append(newLines, lines[:multicastStart+1]...)
			newLines = append(newLines, startPortLine)
			newLines = append(newLines, lines[multicastStart+1:]...)
			lines = newLines
		}
	}

	if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return fmt.Errorf("failed to write cameras config: %w", err)
	}

	return nil
}

// ExtractDefaultCamerasConfig writes default cameras config files
// to the install directory if they don't already exist.
func ExtractDefaultCamerasConfig() string {
	installDir := GetInstallDir()
	if err := os.MkdirAll(installDir, 0755); err != nil {
		logging.ErrorLogger.Printf("Failed to create directory for cameras config files: %v", err)
		return ""
	}

	sharedPath := filepath.Join(installDir, "cameras_config.toml")
	if _, err := os.Stat(sharedPath); os.IsNotExist(err) {
		if err := os.WriteFile(sharedPath, defaultSharedCamerasConfig, 0644); err != nil {
			logging.ErrorLogger.Printf("Failed to write cameras_config.toml: %v", err)
			return ""
		}
		logging.InfoLogger.Printf("Wrote default cameras_config.toml to %s", sharedPath)
	}

	instancePath := filepath.Join(installDir, "cameras.toml")
	if _, err := os.Stat(instancePath); os.IsNotExist(err) {
		if err := os.WriteFile(instancePath, defaultInstanceCamerasConfig, 0644); err != nil {
			logging.ErrorLogger.Printf("Failed to write cameras.toml: %v", err)
			return ""
		}
		logging.InfoLogger.Printf("Wrote default cameras.toml to %s", instancePath)
	}

	return instancePath
}

// filterEncodersForPlatform removes encoder entries that don't match the current OS.
func (c *CamerasConfig) filterEncodersForPlatform() {
	var filtered []EncoderConfig
	for _, enc := range c.Encoders {
		if enc.Platform == "" || enc.Platform == runtime.GOOS {
			filtered = append(filtered, enc)
		}
	}
	c.Encoders = filtered
}

// applyDefaults fills in zero-value fields with sensible defaults.
func (c *CamerasConfig) applyDefaults() {
	if c.Multicast.IP == "" {
		c.Multicast.IP = "239.255.0.1"
	}
	if c.Multicast.StartPort == 0 {
		c.Multicast.StartPort = 9001
	}
	if c.Multicast.PktSize == 0 {
		c.Multicast.PktSize = 1316
	}
	if c.Software.OutputParameters == "" {
		c.Software.OutputParameters = "-c:v libx264 -preset ultrafast -tune zerolatency -b:v 4M"
	}
	if len(c.Cameras.FormatPriority) == 0 {
		c.Cameras.FormatPriority = []string{"h264", "mjpeg"}
	}
	if len(c.Cameras.ModePriority) == 0 {
		c.Cameras.ModePriority = []string{
			"1920x1080@59",
			"1280x720@59",
			"1920x1080@29",
			"1280x720@29",
		}
	}
	if c.Output.GopMultiplier == 0 {
		c.Output.GopMultiplier = 1
	}
	if c.Output.ExtraFlags == "" {
		c.Output.ExtraFlags = "-an -f mpegts"
	}
}

// FormatPriorityValue returns the priority of a pixel format (higher = better).
// Based on the order in [cameras] formatPriority (first = highest).
func (c *CamerasConfig) FormatPriorityValue(pixFmt string) int {
	n := len(c.Cameras.FormatPriority)
	for i, f := range c.Cameras.FormatPriority {
		if f == pixFmt {
			return n - i // first entry gets highest value
		}
	}
	// Raw formats not listed get priority 0
	return 0
}

// ParseModePriority parses the modePriority list into structured entries.
func (c *CamerasConfig) ParseModePriority() []ModePriorityEntry {
	var entries []ModePriorityEntry
	for _, s := range c.Cameras.ModePriority {
		entry, err := parseModePriorityString(s)
		if err != nil {
			logging.WarningLogger.Printf("Ignoring invalid modePriority entry %q: %v", s, err)
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

// ProfilePriorityValue returns the profile priority for a given resolution+fps.
// Higher index in the modePriority list = lower priority (first = best).
func (c *CamerasConfig) ProfilePriorityValue(width, height, fps int) int {
	entries := c.ParseModePriority()
	n := len(entries)
	for i, e := range entries {
		if width == e.Width && height == e.Height && fps >= e.MinFps {
			return n - i // first entry gets highest value
		}
	}
	return 0
}

// parseModePriorityString parses "WIDTHxHEIGHT@FPS" into a ModePriorityEntry.
func parseModePriorityString(s string) (ModePriorityEntry, error) {
	// Expected format: "1920x1080@59"
	atIdx := strings.Index(s, "@")
	if atIdx < 0 {
		return ModePriorityEntry{}, fmt.Errorf("missing @ separator")
	}
	dimPart := s[:atIdx]
	fpsPart := s[atIdx+1:]

	xIdx := strings.Index(dimPart, "x")
	if xIdx < 0 {
		return ModePriorityEntry{}, fmt.Errorf("missing x separator in dimensions")
	}

	w, err := strconv.Atoi(dimPart[:xIdx])
	if err != nil {
		return ModePriorityEntry{}, fmt.Errorf("invalid width: %w", err)
	}
	h, err := strconv.Atoi(dimPart[xIdx+1:])
	if err != nil {
		return ModePriorityEntry{}, fmt.Errorf("invalid height: %w", err)
	}
	f, err := strconv.Atoi(fpsPart)
	if err != nil {
		return ModePriorityEntry{}, fmt.Errorf("invalid fps: %w", err)
	}

	return ModePriorityEntry{Width: w, Height: h, MinFps: f}, nil
}
