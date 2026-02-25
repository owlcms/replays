package replays

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/owlcms/replays/internal/logging"
)

//go:embed config.toml
var defaultConfig []byte

//go:embed default_cameras.toml
var defaultCamerasConfig []byte

// ExtractDefaultConfig extracts the embedded config file if none exists.
func ExtractDefaultConfig(configPath string) error {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		logging.InfoLogger.Printf("No config file found at %s, creating default", configPath)

		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			return fmt.Errorf("failed to create config directory '%s': %w", filepath.Dir(configPath), err)
		}

		if err := os.WriteFile(configPath, defaultConfig, 0644); err != nil {
			return fmt.Errorf("failed to write default config file '%s': %w", configPath, err)
		}

		logging.InfoLogger.Printf("Created default config file at %s", configPath)
	}
	return nil
}

// ExtractDefaultPlatformCamerasConfig extracts the embedded default_cameras.toml
// into the given directory if none exists.
func ExtractDefaultPlatformCamerasConfig(configDir string) error {
	path := filepath.Join(configDir, "default_cameras.toml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("failed to create config directory '%s': %w", filepath.Dir(path), err)
		}

		if err := os.WriteFile(path, defaultCamerasConfig, 0644); err != nil {
			return fmt.Errorf("failed to write default cameras config file '%s': %w", path, err)
		}

		logging.InfoLogger.Printf("Created default cameras config file at %s", path)
	}
	return nil
}
