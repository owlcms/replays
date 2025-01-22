package config

import (
	"os"

	"github.com/BurntSushi/toml"
	"github.com/owlcms/replays/internal/logging"
)

type Config struct {
	Port     int    `toml:"port"`
	VideoDir string `toml:"videoDir"`
}

func LoadConfig() Config {
	var config Config
	config.Port = 8091           // default port
	config.VideoDir = "./videos" // default video directory

	if _, err := os.Stat("env.properties"); err == nil {
		if _, err := toml.DecodeFile("env.properties", &config); err != nil {
			logging.ErrorLogger.Printf("Error reading env.properties: %v", err)
		}
	} else {
		logging.WarningLogger.Println("env.properties file not found, using default configuration")
	}

	return config
}
