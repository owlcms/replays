package main

import (
	"fmt"
	"path/filepath"

	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/monitor"
)

func main() {
	// Ensure logging directory is absolute
	logDir := filepath.Join(config.GetInstallDir(), "logs")

	// Initialize loggers
	if err := logging.Init(logDir); err != nil {
		fmt.Printf("Failed to initialize logging: %v\n", err)
		return
	}

	configFile := filepath.Join(config.GetInstallDir(), "config.toml")
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		return
	}

	monitor.UpdateOwlcmsAddress(cfg, configFile)
}
