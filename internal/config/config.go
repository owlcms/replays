package config

import (
	"os"
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
	FfmpegPath   string `toml:"ffmpegPath"`
	FfmpegCamera string `toml:"ffmpegCamera"`
	FfmpegFormat string `toml:"ffmpegFormat"`
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

	if _, err := os.Stat("config.toml"); err == nil {
		if _, err := toml.DecodeFile("config.toml", &config); err != nil {
			logging.ErrorLogger.Printf("Error reading config.toml: %v", err)
		}
	} else {
		logging.WarningLogger.Println("config.toml file not found, using default configuration")
	}

	if config.FfmpegPath == "" || config.FfmpegCamera == "" || config.FfmpegFormat == "" {
		switch {
		case runtime.GOOS == "windows":
			if config.FfmpegPath == "" {
				config.FfmpegPath = "C:\\ProgramData\\chocolatey\\bin\\ffmpeg.exe"
			}
			if config.FfmpegCamera == "" {
				config.FfmpegCamera = "video=OV01A"
			}
			if config.FfmpegFormat == "" {
				config.FfmpegFormat = "dshow"
			}
		case isWSL():
			if config.FfmpegPath == "" {
				config.FfmpegPath = "/mnt/c/ProgramData/chocolatey/bin/ffmpeg.exe"
			}
			if config.FfmpegCamera == "" {
				config.FfmpegCamera = "video=OV01A"
			}
			if config.FfmpegFormat == "" {
				config.FfmpegFormat = "dshow"
			}
		default: // Linux
			if config.FfmpegPath == "" {
				config.FfmpegPath = "/usr/bin/ffmpeg"
			}
			if config.FfmpegCamera == "" {
				config.FfmpegCamera = "/dev/video0"
			}
			if config.FfmpegFormat == "" {
				config.FfmpegFormat = "v4l2"
			}
		}
	}

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
