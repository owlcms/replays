package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/recording"
)

// Config represents the configuration file structure
type Config struct {
	Port         int    `toml:"port"`
	VideoDir     string `toml:"videoDir"`
	Width        int    `toml:"width"`
	Height       int    `toml:"height"`
	Fps          int    `toml:"fps"`
	Recode       bool   `toml:"recode"` // Add recode option
	FfmpegPath   string
	FfmpegCamera string
	FfmpegFormat string
}

// PlatformConfig represents platform-specific configurations
type PlatformConfig struct {
	FfmpegPath   string `toml:"ffmpegPath"`
	FfmpegCamera string `toml:"ffmpegCamera"`
	FfmpegFormat string `toml:"ffmpegFormat"`
}

var (
	Verbose bool
	NoVideo bool
)

// LoadConfig loads the configuration from the specified file
func LoadConfig(configFile string) (*Config, error) {
	// Extract default config if no config file exists
	if err := ExtractDefaultConfig(configFile); err != nil {
		return nil, fmt.Errorf("failed to extract default config: %w", err)
	}

	var config Config
	var platformConfig struct {
		Windows PlatformConfig `toml:"windows"`
		Linux   PlatformConfig `toml:"linux"`
		WSL     PlatformConfig `toml:"wsl"`
	}

	if _, err := toml.DecodeFile(configFile, &config); err != nil {
		return nil, err
	}

	// Platform-specific configurations
	platform := getPlatformName()
	if _, err := toml.DecodeFile(configFile, &platformConfig); err != nil {
		return nil, err
	}

	switch platform {
	case "windows":
		recording.SetFfmpegConfig(platformConfig.Windows.FfmpegPath, platformConfig.Windows.FfmpegCamera, platformConfig.Windows.FfmpegFormat)
	case "linux":
		recording.SetFfmpegConfig(platformConfig.Linux.FfmpegPath, platformConfig.Linux.FfmpegCamera, platformConfig.Linux.FfmpegFormat)
	case "WSL":
		recording.SetFfmpegConfig(platformConfig.WSL.FfmpegPath, platformConfig.WSL.FfmpegCamera, platformConfig.WSL.FfmpegFormat)
	}

	// Set all recording package configuration
	recording.SetVideoDir(config.VideoDir)
	recording.SetVideoConfig(config.Width, config.Height, config.Fps)

	// Log all configuration parameters
	logging.InfoLogger.Printf("Configuration loaded from %s for platform %s:\n"+
		"    Port: %d\n"+
		"    VideoDir: %s\n"+
		"    Resolution: %dx%d\n"+
		"    FPS: %d\n"+
		"    FFmpeg Path: %s\n"+
		"    FFmpeg Camera: %s\n"+
		"    FFmpeg Format: %s",
		configFile,
		getPlatformName(),
		config.Port,
		config.VideoDir,
		config.Width, config.Height,
		config.Fps,
		recording.FfmpegPath,
		recording.FfmpegCamera,
		recording.FfmpegFormat)

	logging.InfoLogger.Printf("Loaded configuration from %s for platform %s", configFile, getPlatformName())
	return &config, nil
}

// ValidateCamera checks if camera configuration is correct for the platform
func (c *Config) ValidateCamera() error {
	if recording.FfmpegCamera == "" {
		return fmt.Errorf("camera not configured")
	}
	return nil
}

// InitConfig processes command-line flags and loads the configuration
func InitConfig() (*Config, error) {
	configFile := flag.String("config", filepath.Join(GetInstallDir(), "config.toml"), "path to configuration file")
	flag.BoolVar(&Verbose, "v", false, "enable verbose logging")
	flag.BoolVar(&Verbose, "verbose", false, "enable verbose logging")
	flag.BoolVar(&NoVideo, "noVideo", false, "log ffmpeg actions but do not execute them")
	flag.Parse()

	// Initialize loggers
	logging.Init()

	// Load configuration
	cfg, err := LoadConfig(*configFile)
	if err != nil {
		return nil, fmt.Errorf("error loading configuration: %w", err)
	}

	// Set the noVideo flag and recode option in the recording package
	recording.SetNoVideo(NoVideo)
	recording.SetRecode(cfg.Recode)

	return cfg, nil
}

// getInstallDir returns the installation directory based on the environment
func GetInstallDir() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "replays")
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "replays")
	case "linux":
		return filepath.Join(os.Getenv("HOME"), ".local", "share", "replays")
	default:
		return "./replays"
	}
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
