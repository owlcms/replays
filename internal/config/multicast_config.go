package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed multicast.toml
var defaultMulticastConfig []byte

type multicastFile struct {
	Multicast MulticastSettings `toml:"multicast"`
}

func ExtractDefaultMulticastConfig(multicastPath string) error {
	if _, err := os.Stat(multicastPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(multicastPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory '%s': %w", filepath.Dir(multicastPath), err)
		}
		if err := os.WriteFile(multicastPath, defaultMulticastConfig, 0644); err != nil {
			return fmt.Errorf("failed to write default multicast config '%s': %w", multicastPath, err)
		}
	}
	return nil
}

func LoadMulticastConfig(multicastPath string) (MulticastSettings, error) {
	var file multicastFile
	if _, err := toml.DecodeFile(multicastPath, &file); err != nil {
		return MulticastSettings{}, err
	}
	file.Multicast.applyDefaults()
	return file.Multicast, nil
}

// UpdateMulticastMappingFile rewrites multicast.toml with the current mapping.
func UpdateMulticastMappingFile(multicastPath string, settings MulticastSettings) error {
	settings.applyDefaults()

	input, err := os.ReadFile(multicastPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := ExtractDefaultMulticastConfig(multicastPath); err != nil {
				return err
			}
			input = defaultMulticastConfig
		} else {
			return err
		}
	}

	lines := strings.Split(string(input), "\n")
	sectionStart := -1
	sectionEnd := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[multicast]" {
			sectionStart = i
			continue
		}
		if sectionStart >= 0 && sectionStart != i && strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[[") {
			sectionEnd = i
			break
		}
	}

	newSection := []string{
		"# Camera source precedence in replays (highest to lowest):",
		"# 1) multicast.toml mapping (this file, when [multicast].enabled = true in config.toml)",
		"# 2) auto.toml camera sections (when multicast is disabled and auto.toml exists)",
		"# 3) camera sections in config.toml ([windows*], [linux*], etc.)",
		"#",
		"# Note: this precedence applies only to camera source definitions.",
		"# Other application settings still come from config.toml.",
		"",
		"[multicast]",
		fmt.Sprintf("    ip = \"%s\"", settings.IP),
		fmt.Sprintf("    camera1Port = %d", settings.Camera1Port),
		fmt.Sprintf("    camera2Port = %d", settings.Camera2Port),
		fmt.Sprintf("    camera3Port = %d", settings.Camera3Port),
		fmt.Sprintf("    camera4Port = %d", settings.Camera4Port),
	}

	var newLines []string
	if sectionStart >= 0 {
		newLines = append(newLines, lines[:sectionStart]...)
		newLines = append(newLines, newSection...)
		newLines = append(newLines, lines[sectionEnd:]...)
	} else {
		newLines = append(newLines, lines...)
		if len(newLines) > 0 && strings.TrimSpace(newLines[len(newLines)-1]) != "" {
			newLines = append(newLines, "")
		}
		newLines = append(newLines, newSection...)
	}

	return os.WriteFile(multicastPath, []byte(strings.Join(newLines, "\n")), 0644)
}
