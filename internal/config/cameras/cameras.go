package cameras

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/config/ffmpeg"
	"github.com/owlcms/replays/internal/logging"
)

//go:embed config.toml
var defaultInstanceConfig []byte

var configSourcePath string

// Config is the per-instance runtime configuration for the cameras program.
// Encoder/priority settings live in the separate ffmpeg package.
type Config struct {
	Multicast MulticastConfig `toml:"multicast"`
	Unicast   UnicastConfig   `toml:"unicast"`
	Cameras   CamerasSettings `toml:"cameras"`
}

// MulticastConfig holds multicast streaming settings.
type MulticastConfig struct {
	IP        string `toml:"ip"`
	StartPort int    `toml:"startPort"`
	PktSize   int    `toml:"pktSize"`
	LocalOnly bool   `toml:"localOnly"`
}

// UnicastConfig holds unicast tee streaming settings.
// When Enabled is true, each camera stream is sent via ffmpeg tee
// to every address in Destinations, one UDP leg per destination.
type UnicastConfig struct {
	Enabled      bool     `toml:"enabled"`
	StartPort    int      `toml:"startPort"`
	PktSize      int      `toml:"pktSize"`
	Destinations []string `toml:"destinations"`
}

// CamerasSettings holds per-instance camera behaviour flags.
type CamerasSettings struct {
	IncludeAll bool `toml:"includeAll"`
}

// LoadConfig loads the cameras instance configuration from config.toml.
// Search order: exe dir → cwd → install dir → embedded default.
func LoadConfig() (*Config, error) {
	var cfg Config

	baseDirs := []string{}
	if exe, err := os.Executable(); err == nil {
		baseDirs = append(baseDirs, filepath.Dir(exe))
	}
	if cwd, err := os.Getwd(); err == nil {
		baseDirs = append(baseDirs, cwd)
	}
	baseDirs = append(baseDirs, config.GetInstallDir())

	configSourcePath = ""
	for _, dir := range baseDirs {
		path := filepath.Join(dir, "config.toml")
		if _, err := os.Stat(path); err == nil {
			fmt.Printf("Cameras instance config: %s\n", path)
			logging.InfoLogger.Printf("Loading cameras instance config from %s", path)
			if _, err := toml.DecodeFile(path, &cfg); err != nil {
				return nil, fmt.Errorf("failed to parse %s: %w", path, err)
			}
			configSourcePath = path
			break
		}
	}

	if configSourcePath == "" {
		fmt.Println("Cameras instance config: using embedded defaults")
		logging.InfoLogger.Println("No config.toml found, using embedded instance defaults")
		if _, err := toml.Decode(string(defaultInstanceConfig), &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse embedded config.toml: %w", err)
		}
	}

	cfg.applyDefaults()
	return &cfg, nil
}

// GetConfigSourcePath returns the file path used by LoadConfig.
// Empty string means defaults were loaded from embedded config.
func GetConfigSourcePath() string {
	return configSourcePath
}

// SaveMulticastSettings updates multicast.ip, multicast.startPort,
// and multicast.localOnly in the loaded config.toml file.
func SaveMulticastSettings(ip string, startPort int, localOnly bool) error {
	if strings.TrimSpace(ip) == "" {
		return fmt.Errorf("invalid multicast ip")
	}
	if startPort < 1 || startPort > 65535 {
		return fmt.Errorf("invalid startPort %d", startPort)
	}

	configPath := GetConfigSourcePath()
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

	ipLine := fmt.Sprintf("    ip = \"%s\"", ip)
	startPortLine := fmt.Sprintf("    startPort = %d", startPort)
	localOnlyLine := fmt.Sprintf("    localOnly = %t", localOnly)

	if multicastStart == -1 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines,
			"[multicast]",
			ipLine,
			startPortLine,
			localOnlyLine,
		)
	} else {
		updatedIP := false
		updatedStartPort := false
		updatedLocalOnly := false
		for i := multicastStart + 1; i < multicastEnd; i++ {
			trimmed := strings.TrimSpace(lines[i])
			if strings.HasPrefix(trimmed, "ip") {
				indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
				lines[i] = fmt.Sprintf("%sip = \"%s\"", indent, ip)
				updatedIP = true
				continue
			}
			if strings.HasPrefix(trimmed, "startPort") {
				indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
				lines[i] = fmt.Sprintf("%sstartPort = %d", indent, startPort)
				updatedStartPort = true
				continue
			}
			if strings.HasPrefix(trimmed, "localOnly") {
				indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
				lines[i] = fmt.Sprintf("%slocalOnly = %t", indent, localOnly)
				updatedLocalOnly = true
			}
		}
		if !updatedIP || !updatedStartPort || !updatedLocalOnly {
			newLines := make([]string, 0, len(lines)+3)
			newLines = append(newLines, lines[:multicastStart+1]...)
			if !updatedIP {
				newLines = append(newLines, ipLine)
			}
			if !updatedStartPort {
				newLines = append(newLines, startPortLine)
			}
			if !updatedLocalOnly {
				newLines = append(newLines, localOnlyLine)
			}
			newLines = append(newLines, lines[multicastStart+1:]...)
			lines = newLines
		}
	}

	if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		return fmt.Errorf("failed to write cameras config: %w", err)
	}

	return nil
}

// SaveStartPort updates multicast.startPort in the loaded config.toml file.
func SaveStartPort(startPort int) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	return SaveMulticastSettings(cfg.Multicast.IP, startPort, cfg.Multicast.LocalOnly)
}

// ExtractDefaultConfig writes the default cameras config.toml
// to the install directory if it doesn't already exist.
// Also ensures ffmpeg.toml is extracted via the ffmpeg package.
func ExtractDefaultConfig() string {
	installDir := config.GetInstallDir()
	if err := os.MkdirAll(installDir, 0755); err != nil {
		logging.ErrorLogger.Printf("Failed to create directory for cameras config files: %v", err)
		return ""
	}

	// Ensure ffmpeg.toml exists in the shared config directory
	if p := ffmpeg.ExtractDefaultConfig(); p == "" {
		logging.WarningLogger.Println("Failed to extract default ffmpeg.toml")
	}

	instancePath := filepath.Join(installDir, "config.toml")
	if _, err := os.Stat(instancePath); os.IsNotExist(err) {
		if err := os.WriteFile(instancePath, defaultInstanceConfig, 0644); err != nil {
			logging.ErrorLogger.Printf("Failed to write config.toml: %v", err)
			return ""
		}
		logging.InfoLogger.Printf("Wrote default config.toml to %s", instancePath)
	}

	return instancePath
}

// applyDefaults fills in zero-value fields with sensible defaults.
func (c *Config) applyDefaults() {
	if c.Multicast.IP == "" {
		c.Multicast.IP = "239.255.0.1"
	}
	if c.Multicast.StartPort == 0 {
		c.Multicast.StartPort = 9001
	}
	if c.Multicast.PktSize == 0 {
		c.Multicast.PktSize = 1316
	}
	// Unicast defaults
	if c.Unicast.StartPort == 0 {
		c.Unicast.StartPort = 9001
	}
	if c.Unicast.PktSize == 0 {
		c.Unicast.PktSize = 1316
	}
}

// UnicastTeeOutput builds the ffmpeg -f tee output string for a given port.
// Each destination gets its own "[f=mpegts:onfail=ignore]udp://ip:port?pkt_size=N" leg.
func (c *UnicastConfig) TeeOutput(port int) string {
	var legs []string
	for _, dest := range c.Destinations {
		leg := fmt.Sprintf("[f=mpegts:onfail=ignore]udp://%s:%d?pkt_size=%d", dest, port, c.PktSize)
		legs = append(legs, leg)
	}
	return strings.Join(legs, "|")
}
