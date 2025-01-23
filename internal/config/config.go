package config

import (
	"os"

	"github.com/BurntSushi/toml"
	"github.com/owlcms/replays/internal/logging"
)

type Config struct {
	Port     int    `toml:"port"`
	VideoDir string `toml:"videoDir"`
	Width    int    `toml:"width"`     // Video width in pixels
	Height   int    `toml:"height"`    // Video height in pixels
	FPS      int    `toml:"fps"`       // Frames per second
}

func LoadConfig() Config {
	var config Config
	config.Port = 8091           // default port
	config.VideoDir = "./videos" // default video directory
	config.Width = 1280          // default width
	config.Height = 720          // default height
	config.FPS = 30              // default FPS

	if _, err := os.Stat("env.properties"); err == nil {
		if _, err := toml.DecodeFile("env.properties", &config); err != nil {
			logging.ErrorLogger.Printf("Error reading env.properties: %v", err)
		}
	} else {
		logging.WarningLogger.Println("env.properties file not found, using default configuration")
	}

	return config
}
