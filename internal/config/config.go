package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/owlcms/replays/internal/logging"
)

type Config struct {
	Port         int    `toml:"port"`
	VideoDir     string `toml:"videoDir"`
	Width        int    `toml:"width"`  // Video width in pixels
	Height       int    `toml:"height"` // Video height in pixels
	FPS          int    `toml:"fps"`    // Frames per second
	FfmpegPath   string
	FfmpegCamera string
	FfmpegFormat string
}

func isWSL() bool {
	if data, err := os.ReadFile("/proc/version"); err == nil {
		return strings.Contains(strings.ToLower(string(data)), "wsl")
	}
	return false
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

	// Set ffmpeg defaults based on OS
	switch {
	case runtime.GOOS == "windows":
		config.FfmpegPath = filepath.Clean("C:/ProgramData/chocolatey/bin/ffmpeg.exe")
		config.FfmpegCamera = "video=OV01A"
		config.FfmpegFormat = "dshow"
	case isWSL():
		config.FfmpegPath = "/mnt/c/ProgramData/chocolatey/bin/ffmpeg.exe"
		config.FfmpegCamera = "video=OV01A"
		config.FfmpegFormat = "dshow"
	default: // Linux
		config.FfmpegPath = "/usr/bin/ffmpeg"
		config.FfmpegCamera = "/dev/video0"
		config.FfmpegFormat = "v4l2"
	}

	// Override from environment variables if present
	if path := os.Getenv("FFMPEG_PATH"); path != "" {
		config.FfmpegPath = path
	}
	if camera := os.Getenv("FFMPEG_CAMERA"); camera != "" {
		config.FfmpegCamera = camera
	}
	if format := os.Getenv("FFMPEG_FORMAT"); format != "" {
		config.FfmpegFormat = format
	}

	return config
}
