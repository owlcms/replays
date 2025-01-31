package main

import (
	"fmt"
	"path/filepath"

	"github.com/owlcms/replays/internal/config"  // Adjust the import path as necessary
	"github.com/owlcms/replays/internal/logging" // Adjust the import path as necessary
	"github.com/owlcms/replays/internal/mqtt"    // Adjust the import path as necessary
)

func main() {
	// Define a command-line flag
	// scan := flag.Bool("scan", false, "Scan the local network for MQTT brokers")
	// flag.Parse()

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

	mqtt.UpdateOwlcmsAddress(cfg, configFile)
}
