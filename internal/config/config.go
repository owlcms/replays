package config

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/videos"
)

// Config represents the configuration file structure
type Config struct {
	Port     int    `toml:"port"`
	VideoDir string `toml:"videoDir"`
	Width    int    `toml:"width"`
	Height   int    `toml:"height"`
	Fps      int    `toml:"fps"`
	Recode   bool   `toml:"recode"` // Add recode option

	// Platform-specific configurations
	Linux   FFmpegConfig `toml:"linux"`
	Windows FFmpegConfig `toml:"windows"`
	WSL     FFmpegConfig `toml:"wsl"`
}

// FFmpegConfig holds ffmpeg-specific settings
type FFmpegConfig struct {
	FfmpegPath   string `toml:"ffmpegPath"`
	FfmpegCamera string `toml:"ffmpegCamera"`
	FfmpegFormat string `toml:"ffmpegFormat"`
}

// LoadConfig loads the configuration from the specified file
func LoadConfig(configFile string) (*Config, error) {
	// Extract default config if no config file exists
	if err := ExtractDefaultConfig(configFile); err != nil {
		return nil, fmt.Errorf("failed to extract default config: %w", err)
	}

	var config Config

	if _, err := toml.DecodeFile(configFile, &config); err != nil {
		return nil, err
	}

	// Determine platform and select appropriate ffmpeg config
	var ffmpegConfig FFmpegConfig
	platform := strings.ToLower(getPlatformName())
	switch platform {
	case "windows":
		ffmpegConfig = config.Windows
	case "wsl":
		ffmpegConfig = config.WSL
	default: // linux
		ffmpegConfig = config.Linux
	}

	// Set all video package configuration
	videos.SetVideoDir(config.VideoDir)
	videos.SetVideoConfig(config.Width, config.Height, config.Fps)
	videos.SetFfmpegConfig(ffmpegConfig.FfmpegPath, ffmpegConfig.FfmpegCamera, ffmpegConfig.FfmpegFormat)

	// Log all configuration parameters
	logging.InfoLogger.Printf("Configuration loaded for platform %s:\n"+
		"    Port: %d\n"+
		"    VideoDir: %s\n"+
		"    Resolution: %dx%d\n"+
		"    FPS: %d\n"+
		"    FFmpeg Path: %s\n"+
		"    FFmpeg Camera: %s\n"+
		"    FFmpeg Format: %s",
		platform,
		config.Port,
		config.VideoDir,
		config.Width, config.Height,
		config.Fps,
		ffmpegConfig.FfmpegPath,
		ffmpegConfig.FfmpegCamera,
		ffmpegConfig.FfmpegFormat)

	logging.InfoLogger.Printf("Loaded configuration from %s for platform %s", configFile, getPlatformName())
	return &config, nil
}

// ValidateCamera checks if camera configuration is correct for the platform
func (c *Config) ValidateCamera() error {
	// Get current OS
	os := runtime.GOOS

	// Check camera config for Windows and WSL
	if os == "windows" {
		if !strings.HasPrefix(c.Windows.FfmpegCamera, "video=") {
			return fmt.Errorf("windows camera name must start with 'video=', current: %s", c.Windows.FfmpegCamera)
		}
	} else if os == "linux" && isWSL() {
		if !strings.HasPrefix(c.WSL.FfmpegCamera, "video=") {
			return fmt.Errorf("wsl camera name must start with 'video=', current: %s", c.WSL.FfmpegCamera)
		}
	}
	return nil
}

// isWSL checks if we're running under Windows Subsystem for Linux
func isWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}

	// Check for WSL-specific file
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "microsoft") ||
		strings.Contains(strings.ToLower(string(data)), "wsl")
}

// getPlatformName returns a string describing the current platform
func getPlatformName() string {
	if isWSL() {
		return "WSL"
	}
	return runtime.GOOS
}
