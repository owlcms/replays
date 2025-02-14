package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/owlcms/replays/internal/logging"
)

// CameraConfiguration represents platform-specific configurations
type CameraConfiguration struct {
	FfmpegPath   string `toml:"ffmpegPath"`
	FfmpegCamera string `toml:"ffmpegCamera"`
	Format       string `toml:"format"`
	Params       string `toml:"params"`
	Size         string `toml:"size"`
	Fps          int    `toml:"fps"`
	Recode       bool   `toml:"recode"` // Add recode field
}

// Config represents the configuration file structure
type Config struct {
	Port     int                   `toml:"port"`
	VideoDir string                `toml:"videoDir"`
	Width    int                   `toml:"width"`
	Height   int                   `toml:"height"`
	Fps      int                   `toml:"fps"`
	OwlCMS   string                `toml:"owlcms"`
	Platform string                `toml:"platform"`
	Cameras  []CameraConfiguration `toml:"-"`
}

var (
	Verbose       bool
	NoVideo       bool
	InstallDir    string
	videoDir      string
	Width         int
	Height        int
	Fps           int
	Recode        bool
	CameraConfigs []CameraConfiguration
	currentConfig *Config
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

	// Load all raw config data for camera configs
	var raw map[string]interface{}
	if _, err := toml.DecodeFile(configFile, &raw); err != nil {
		return nil, err
	}

	platformKey := getPlatformName()
	var cameras []CameraConfiguration

	// Helper: decode a raw map into a PlatformConfig
	decodePlatformConfig := func(data interface{}) (CameraConfiguration, error) {
		m, ok := data.(map[string]interface{})
		if !ok {
			return CameraConfiguration{}, fmt.Errorf("invalid type for platform config")
		}
		var pc CameraConfiguration
		if val, ok := m["ffmpegPath"].(string); ok {
			pc.FfmpegPath = val
		}
		if val, ok := m["ffmpegCamera"].(string); ok {
			pc.FfmpegCamera = val
		}
		if val, ok := m["format"].(string); ok {
			pc.Format = val
		}
		if val, ok := m["params"].(string); ok {
			pc.Params = val
		}
		if val, ok := m["size"].(string); ok {
			pc.Size = val
		}
		if val, ok := m["fps"].(int64); ok {
			pc.Fps = int(val)
		}
		if val, ok := m["recode"].(bool); ok {
			pc.Recode = val
		} else {
			pc.Recode = false // Default to false
		}
		return pc, nil
	}

	// Look for keys: "platformKey", "platformKey2", "platformKey3", ...
	for i := 1; ; i++ {
		key := platformKey
		if i > 1 {
			key = platformKey + strconv.Itoa(i)
		}
		confRaw, exists := raw[key]
		if !exists {
			// Check for aliases
			if platformKey == "windows" && i == 1 {
				confRaw, exists = raw["windows1"]
			} else if platformKey == "linux" && i == 1 {
				confRaw, exists = raw["linux1"]
			}
			if !exists {
				break
			}
		}
		pc, err := decodePlatformConfig(confRaw)
		if err != nil {
			return nil, err
		}
		cameras = append(cameras, pc)
	}
	config.Cameras = cameras

	// Set remaining recording package configurations
	SetVideoDir(config.VideoDir)
	SetVideoConfig(config.Width, config.Height, config.Fps)

	// Log all configuration parameters including all cameras
	platformKey = getPlatformName()
	logging.InfoLogger.Printf("Configuration loaded from %s for platform %s:\n"+
		"    Port: %d\n"+
		"    VideoDir: %s\n",
		configFile,
		platformKey,
		config.Port,
		config.VideoDir)

	// Log each camera configuration
	for i, camera := range cameras {
		suffix := ""
		if i > 0 {
			suffix = strconv.Itoa(i + 1)
		}
		logging.InfoLogger.Printf("Camera configuration for %s%s:\n"+
			"    FFmpeg Path: %s\n"+
			"    FFmpeg Camera: %s\n"+
			"    Format: %s\n"+
			"    Params: %s\n"+
			"    Size: %s\n"+
			"    FPS: %d\n"+
			"    Recode: %t",
			platformKey, suffix,
			camera.FfmpegPath,
			camera.FfmpegCamera,
			camera.Format,
			camera.Params,
			camera.Size,
			camera.Fps,
			camera.Recode)
	}

	// Store the current config for later use
	currentConfig = &config

	return &config, nil
}

// GetCurrentConfig returns the current configuration
func GetCurrentConfig() *Config {
	return currentConfig
}

// ValidateCamera checks if camera configuration is correct for the platform
func (c *Config) ValidateCamera() error {
	if len(c.Cameras) == 0 || c.Cameras[0].FfmpegCamera == "" {
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

	// Set camera configurations in the config package
	SetCameraConfigs(cfg.Cameras)

	return cfg, nil
}

// SetCameraConfigs sets the available camera configurations.
func SetCameraConfigs(configs []CameraConfiguration) {
	CameraConfigs = configs
}

// GetCameraConfigs returns the current camera configurations
func GetCameraConfigs() []CameraConfiguration {
	return CameraConfigs
}

// getInstallDir returns the installation directory based on the environment
func GetInstallDir() string {
	if InstallDir != "" && filepath.IsAbs(InstallDir) {
		return InstallDir
	}

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

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# owlcms =") ||
			strings.HasPrefix(trimmed, "owlcms =") ||
			trimmed == "# owlcms" {
			address := owlcmsAddress
			if strings.Contains(address, ":") {
				address = strings.Split(address, ":")[0]
			}
			leadingSpace := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = fmt.Sprintf("%sowlcms = \"%s\"", leadingSpace, address)
			foundOwlcms = true
			break
		}
		if strings.HasPrefix(trimmed, "port") {
			portLineIndex = i
		}
	}

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
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "owlcms") {
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

// SetVideoDir sets the video directory
func SetVideoDir(dir string) {
	videoDir = dir
}

// SetVideoConfig sets the video configuration
func SetVideoConfig(width, height, fps int) {
	Width = width
	Height = height
	Fps = fps
}

// SetNoVideo sets the no video flag
func SetNoVideo(noVideo bool) {
	NoVideo = noVideo
}

// SetRecode sets the recode flag
func SetRecode(recode bool) {
	Recode = recode
}

// GetVideoDir returns the video directory
func GetVideoDir() string {
	return videoDir
}
