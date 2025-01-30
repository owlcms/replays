package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/owlcms/replays/internal/config"  // Adjust the import path as necessary
	"github.com/owlcms/replays/internal/logging" // Adjust the import path as necessary
	"github.com/owlcms/replays/internal/mqtt"    // Adjust the import path as necessary
)

func isPortOpen(address string) bool {
	logging.InfoLogger.Printf("Checking if port is open: %s", address)
	timeout := 100 * time.Millisecond
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	return true
}

func updateConfigFile(configFile, owlcmsAddress string) error {
	content, err := os.ReadFile(configFile)
	if err != nil {
		return err
	}

	lines := strings.Split(string(content), "\n")
	foundOwlcms := false
	portLineIndex := -1

	// Find and replace the owlcms line, preserving comments and structure
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# owlcms =") ||
			strings.HasPrefix(trimmed, "owlcms =") ||
			trimmed == "# owlcms" {
			// Remove port from address if present
			address := owlcmsAddress
			if strings.Contains(address, ":") {
				address = strings.Split(address, ":")[0]
			}
			// Preserve any leading whitespace
			leadingSpace := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = fmt.Sprintf("%sowlcms = \"%s\"", leadingSpace, address)
			foundOwlcms = true
			break
		}
		if strings.HasPrefix(trimmed, "port") {
			portLineIndex = i
		}
	}

	// If owlcms line not found, add it after the port line
	if !foundOwlcms && portLineIndex >= 0 {
		address := owlcmsAddress
		if strings.Contains(address, ":") {
			address = strings.Split(address, ":")[0]
		}
		leadingSpace := lines[portLineIndex][:len(lines[portLineIndex])-len(strings.TrimLeft(lines[portLineIndex], " \t"))]
		newLine := fmt.Sprintf("%sowlcms = \"%s\"", leadingSpace, address)
		lines = append(lines[:portLineIndex+1], append([]string{newLine}, lines[portLineIndex+1:]...)...)
	}

	return os.WriteFile(configFile, []byte(strings.Join(lines, "\n")), 0644)
}

func main() {
	// Define a command-line flag
	scan := flag.Bool("scan", false, "Scan the local network for MQTT brokers")
	flag.Parse()

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

	owlcmsAddress := fmt.Sprintf("%s:1883", cfg.OwlCMS)
	if cfg.OwlCMS != "" && isPortOpen(owlcmsAddress) {
		fmt.Printf("OwlCMS broker is reachable at %s\n", owlcmsAddress)
	} else {
		fmt.Printf("OwlCMS broker is not reachable at %s, scanning for brokers...\n", owlcmsAddress)
		if *scan {
			broker, err := mqtt.DiscoverBroker()
			if err != nil {
				fmt.Printf("Error discovering broker: %v\n", err)
				return
			}
			fmt.Printf("Broker found: %s\n", broker)
			if err := updateConfigFile(configFile, broker); err != nil {
				fmt.Printf("Error updating config file: %v\n", err)
			}
		} else {
			fmt.Println("Usage: go run main.go -scan")
		}
	}
}
