package config

import (
	"log"
	"os"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Port int `toml:"port"`
}

func LoadConfig() Config {
	var config Config
	config.Port = 8091 // default port

	if _, err := os.Stat("env.properties"); err == nil {
		if _, err := toml.DecodeFile("env.properties", &config); err != nil {
			log.Printf("Error reading env.properties: %v", err)
		}
	} else {
		log.Println("env.properties file not found, using default configuration")
	}

	return config
}
