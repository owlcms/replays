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
	Port     int    `toml:"port"`
	VideoDir string `toml:"videoDir"`
	Width    int    `toml:"width"`
	Height   int    `toml:"height"`
	Fps      int    `toml:"fps"`
	Recode   bool   `toml:"recode"`
	OwlCMS   string `toml:"owlcms"`
	Platform string `toml:"platform"` // Add platform parameter
}

// PlatformConfig represents platform-specific configurations
type PlatformConfig struct {
	FfmpegPath   string `toml:"ffmpegPath"`
	FfmpegCamera string `toml:"ffmpegCamera"`
	FfmpegFormat string `toml:"ffmpegFormat"`
	FfmpegParams string `toml:"ffmpegParams"` // Add new field for ffmpeg parameters
}

var (
	Verbose    bool
	NoVideo    bool
	InstallDir string // Add new variable for installation directory
)

// LoadConfig loads the configuration from the specified file
func LoadConfig(configFile string) (*Config, error) {
	// Ensure InstallDir is initialized
	if InstallDir == "" {
		InstallDir = GetInstallDir()
	}

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

	// Ensure VideoDir is absolute and default to "videos" if not specified
	if config.VideoDir == "" {
		config.VideoDir = "videos"
	}
	if !filepath.IsAbs(config.VideoDir) {
		config.VideoDir = filepath.Join(GetInstallDir(), config.VideoDir)
	}

	// Create VideoDir if it doesn't exist
	if err := os.MkdirAll(config.VideoDir, os.ModePerm); err != nil {
		return nil, fmt.Errorf("failed to create video directory: %w", err)
	}

	// Log the video directory
	logging.InfoLogger.Printf("Videos will be stored in: %s", config.VideoDir)

	// Platform-specific configurations
	platform := getPlatformName()
	if _, err := toml.DecodeFile(configFile, &platformConfig); err != nil {
		return nil, err
	}

	switch platform {
	case "windows":
		recording.SetFfmpegConfig(
			platformConfig.Windows.FfmpegPath,
			platformConfig.Windows.FfmpegCamera,
			platformConfig.Windows.FfmpegFormat,
			platformConfig.Windows.FfmpegParams,
		)
	case "linux":
		recording.SetFfmpegConfig(
			platformConfig.Linux.FfmpegPath,
			platformConfig.Linux.FfmpegCamera,
			platformConfig.Linux.FfmpegFormat,
			platformConfig.Linux.FfmpegParams,
		)
	case "WSL":
		recording.SetFfmpegConfig(
			platformConfig.WSL.FfmpegPath,
			platformConfig.WSL.FfmpegCamera,
			platformConfig.WSL.FfmpegFormat,
			platformConfig.WSL.FfmpegParams,
		)
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
	flag.StringVar(&InstallDir, "dir", "replays", fmt.Sprintf(
		`Name of an alternate installation directory. Default is 'replays'.
Value is relative to the platform-specific directory for applcation data (%s)
Used for multiple installations on the same machine (e.g. 'replays2, replay3').
An absolute path can be provded if needed.`, GetInstallDir()))
	flag.BoolVar(&Verbose, "v", false, "enable verbose logging")
	flag.BoolVar(&Verbose, "verbose", false, "enable verbose logging")
	flag.BoolVar(&NoVideo, "noVideo", false, "log ffmpeg actions but do not execute them")
	flag.Parse()

	// Ensure logging directory is absolute
	logDir := filepath.Join(GetInstallDir(), "logs")

	// Initialize loggers
	if err := logging.Init(logDir); err != nil {
		return nil, fmt.Errorf("failed to initialize logging: %w", err)
	}

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
	// If InstallDir is set and is absolute, use it directly
	if InstallDir != "" && filepath.IsAbs(InstallDir) {
		return InstallDir
	}

	// Get the platform-specific base directory and app name
	var baseDir string
	appName := "replays"
	if InstallDir != "" {
		appName = InstallDir
	}

	switch runtime.GOOS {
	case "windows":
		baseDir = filepath.Join(os.Getenv("APPDATA"), appName)
	case "darwin":
		baseDir = filepath.Join(os.Getenv("HOME"), "Library", "Application Support", appName)
	case "linux":
		baseDir = filepath.Join(os.Getenv("HOME"), ".local", "share", appName)
	default:
		baseDir = "./" + appName
	}

	return baseDir
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

func UpdateConfigFile(configFile, owlcmsAddress string) error {
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

// UpdatePlatform updates the platform in the config file while preserving comments and ordering
func UpdatePlatform(configFile, platform string) error {
	input, err := os.ReadFile(configFile)
	if err != nil {
		return fmt.Errorf("failed to read config file: %v", err)
	}

	lines := strings.Split(string(input), "\n")
	platformFound := false

	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "platform") {
			lines[i] = fmt.Sprintf("platform = \"%s\"", platform)
			platformFound = true
			break
		}
	}

	if !platformFound {
		// If platform line doesn't exist, add it after owlcms line
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "owlcms") {
				// Insert platform after owlcms line
				newLines := make([]string, 0, len(lines)+1)
				newLines = append(newLines, lines[:i+1]...)
				newLines = append(newLines, fmt.Sprintf("platform = \"%s\"", platform))
				newLines = append(newLines, lines[i+1:]...)
				lines = newLines
				break
			}
		}
	}

	output := strings.Join(lines, "\n")
	if err := os.WriteFile(configFile, []byte(output), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %v", err)
	}

	return nil
}
