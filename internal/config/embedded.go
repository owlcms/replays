package config

import (
	_ "embed"
	"os"
	"path/filepath"

	"github.com/owlcms/replays/internal/logging"
)

//go:embed default.toml
var defaultConfig []byte

// ExtractDefaultConfig extracts the embedded config file if none exists
func ExtractDefaultConfig(configPath string) error {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		logging.InfoLogger.Printf("No config file found at %s, creating default", configPath)

		// Ensure directory exists
		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			return err
		}

		// Write default config
		if err := os.WriteFile(configPath, defaultConfig, 0644); err != nil {
			return err
		}

		logging.InfoLogger.Printf("Created default config file at %s", configPath)
	}
	return nil
}
