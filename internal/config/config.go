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
	FfmpegPath       string `toml:"ffmpegPath"`
	FfmpegCamera     string `toml:"ffmpegCamera"`
	Format           string `toml:"format"`
	Params           string `toml:"params"`
	InputParameters  string `toml:"inputParameters"`
	OutputParameters string `toml:"outputParameters"`
	Size             string `toml:"size"`
	Fps              int    `toml:"fps"`
	Recode           bool   `toml:"recode"` // Add recode field
}

// MulticastSettings holds the multicast camera configuration.
// When Enabled is true, replays reads H.264 streams from multicast UDP
// instead of using locally-attached cameras.
type MulticastSettings struct {
	Enabled     bool   `toml:"enabled"`
	IP          string `toml:"ip"`
	Camera1Port int    `toml:"camera1Port"`
	Camera2Port int    `toml:"camera2Port"`
	Camera3Port int    `toml:"camera3Port"`
	Camera4Port int    `toml:"camera4Port"`
}

// Config represents the configuration file structure
type Config struct {
	Port      int                   `toml:"port"`
	VideoDir  string                `toml:"videoDir"`
	Width     int                   `toml:"width"`
	Height    int                   `toml:"height"`
	Fps       int                   `toml:"fps"`
	OwlCMS    string                `toml:"owlcms"`
	Platform  string                `toml:"platform"`
	LogFfmpeg bool                  `toml:"logFfmpeg"`
	Multicast MulticastSettings     `toml:"multicast"`
	Cameras   []CameraConfiguration `toml:"-"`
}

var (
	Verbose       bool
	NoVideo       bool
	NoMQTT        bool
	AutoTomlDir   string
	ConfigDir     string
	InstallDir    string
	videoDir      string
	Width         int
	Height        int
	Fps           int
	Recode        bool
	LogFfmpeg     bool
	Mjpeg720pOnly = IsLinuxARM()
	CameraConfigs []CameraConfiguration
	currentConfig *Config
	ffmpegPath    string // Store the located ffmpeg path
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
		return nil, fmt.Errorf("failed to parse config file '%s': %w\n\nPlease check the file syntax and ensure all values are properly formatted", configFile, err)
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
		return nil, fmt.Errorf("failed to create video directory '%s': %w", config.VideoDir, err)
	}

	// Log the video directory
	logging.InfoLogger.Printf("Videos will be stored in: %s", config.VideoDir)

	// Load all raw config data for camera configs
	var raw map[string]interface{}
	if _, err := toml.DecodeFile(configFile, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse config file for camera configurations: %w", err)
	}

	// Default multicast to enabled when key is absent (for backward compatibility).
	if !hasMulticastEnabledKey(raw) {
		config.Multicast.Enabled = true
	}

	platformKey := getPlatformName()
	var cameras []CameraConfiguration

	// Helper: decode a raw map into a PlatformConfig
	decodePlatformConfig := func(sectionName string, data interface{}) (CameraConfiguration, error) {
		m, ok := data.(map[string]interface{})
		if !ok {
			return CameraConfiguration{}, fmt.Errorf("invalid type for platform config")
		}
		var pc CameraConfiguration
		if val, ok := m["enabled"].(bool); ok && !val {
			logging.InfoLogger.Printf("Camera configuration for section %s is disabled", sectionName)
			return CameraConfiguration{}, fmt.Errorf("camera configuration for section %s is disabled", sectionName)
		}
		if val, ok := m["ffmpegPath"].(string); ok {
			pc.FfmpegPath = val
		}
		if val, ok := m["ffmpegCamera"].(string); ok {
			pc.FfmpegCamera = val
		}
		if val, ok := m["camera"].(string); ok {
			pc.FfmpegCamera = val
		}
		if val, ok := m["format"].(string); ok {
			pc.Format = val
		}
		if val, ok := m["params"].(string); ok {
			pc.Params = val
		}
		if val, ok := m["inputParameters"].(string); ok {
			pc.InputParameters = val
		}
		if val, ok := m["outputParameters"].(string); ok {
			pc.OutputParameters = val
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

		// Add "video=" to ffmpegCamera if format is dshow and ffmpegCamera does not start with "video="
		if pc.Format == "dshow" && !strings.HasPrefix(pc.FfmpegCamera, "video=") {
			pc.FfmpegCamera = "video=" + pc.FfmpegCamera
		}

		return pc, nil
	}

	if config.Multicast.Enabled {
		multicastPath := filepath.Join(GetInstallDir(), "multicast.toml")
		if err := ExtractDefaultMulticastConfig(multicastPath); err != nil {
			return nil, fmt.Errorf("failed to extract default multicast config: %w", err)
		}

		multicastSettings, err := LoadMulticastConfig(multicastPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load multicast config '%s': %w", multicastPath, err)
		}

		// Keep enabled state from config.toml, load mapping values from multicast.toml
		config.Multicast.IP = multicastSettings.IP
		config.Multicast.Camera1Port = multicastSettings.Camera1Port
		config.Multicast.Camera2Port = multicastSettings.Camera2Port
		config.Multicast.Camera3Port = multicastSettings.Camera3Port
		config.Multicast.Camera4Port = multicastSettings.Camera4Port

		multicastCameras := config.Multicast.buildCameraConfigs()
		if len(multicastCameras) == 0 {
			return nil, fmt.Errorf("multicast is enabled but no camera ports are configured in %s", multicastPath)
		}
		cameras = multicastCameras
		logging.InfoLogger.Printf("Multicast mode enabled: loaded %d multicast camera(s) from %s", len(multicastCameras), multicastPath)
	} else {
		// Multicast off: check for auto.toml first (takes precedence over config.toml camera sections)
		autoTomlPath := filepath.Join(GetInstallDir(), "auto.toml")
		if _, err := os.Stat(autoTomlPath); err == nil {
			logging.InfoLogger.Printf("Found auto.toml, loading camera configurations from it")
			var autoRaw map[string]interface{}
			if _, err := toml.DecodeFile(autoTomlPath, &autoRaw); err == nil {
				// Look for camera1, camera2, camera3, ...
				for i := 1; ; i++ {
					key := "camera" + strconv.Itoa(i)
					confRaw, exists := autoRaw[key]
					if !exists {
						break
					}
					pc, err := decodePlatformConfig(key, confRaw)
					if err != nil {
						continue
					}
					cameras = append(cameras, pc)
				}
				if len(cameras) > 0 {
					logging.InfoLogger.Printf("Loaded %d camera configurations from auto.toml", len(cameras))
				}
			} else {
				logging.ErrorLogger.Printf("Failed to parse auto.toml: %v", err)
			}
		}

		// Fall back to platform-specific sections in config.toml if auto.toml didn't provide cameras
		if len(cameras) == 0 {
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
				pc, err := decodePlatformConfig(key, confRaw)
				if err != nil {
					continue // Skip disabled camera configurations
				}
				cameras = append(cameras, pc)
			}
		}
	}

	config.Cameras = cameras

	// Apply multicast defaults
	config.Multicast.applyDefaults()

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

	// Set the global LogFfmpeg flag
	LogFfmpeg = config.LogFfmpeg

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
	configFile := flag.String("config", "", "path to configuration file")
	flag.StringVar(&ConfigDir, "configDir", "", "directory containing editable config files (config.toml, auto.toml, multicast.toml, etc.)")
	flag.StringVar(&InstallDir, "dir", "replays", fmt.Sprintf(
		`Name of an alternate installation directory. Default is 'replays'.
Value is relative to the platform-specific directory for applcation data (%s)
Used for multiple installations on the same machine (e.g. 'replays2, replay3').
An absolute path can be provded if needed.`, GetInstallDir()))
	verbose := flag.Bool("v", false, "enable verbose logging")
	verboseAlt := flag.Bool("verbose", false, "enable verbose logging")
	flag.BoolVar(&NoVideo, "noVideo", false, "log ffmpeg actions but do not execute them")
	flag.BoolVar(&NoMQTT, "noMQTT", false, "disable MQTT autodiscovery and monitoring")
	flag.StringVar(&AutoTomlDir, "autoTomlDir", "", "directory for auto.toml output (default: install dir)")
	flag.Parse()

	if ConfigDir != "" {
		absConfigDir, err := filepath.Abs(ConfigDir)
		if err != nil {
			return nil, fmt.Errorf("invalid configDir '%s': %w", ConfigDir, err)
		}
		ConfigDir = absConfigDir
	}

	if *configFile == "" {
		*configFile = filepath.Join(GetInstallDir(), "config.toml")
	}

	// Set verbose mode in logging package
	logging.SetVerbose(*verbose || *verboseAlt)

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
	if ConfigDir != "" {
		return ConfigDir
	}

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
		// TEMPORARY: Replace 'lamyj' with 'le test' in the path for testing
		//baseDir = strings.ReplaceAll(baseDir, "lamyj", "le test")
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

// IsLinuxARM reports whether the process is running on Linux ARM/ARM64.
func IsLinuxARM() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	return runtime.GOARCH == "arm" || runtime.GOARCH == "arm64"
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

// SetFFmpegPath sets the ffmpeg executable path
func SetFFmpegPath(path string) {
	ffmpegPath = path
	logging.InfoLogger.Printf("FFmpeg path set to: %s", path)
}

// GetFFmpegPath returns the ffmpeg executable path
func GetFFmpegPath() string {
	return ffmpegPath
}

// GetLogFfmpeg returns the logFfmpeg setting
func GetLogFfmpeg() bool {
	return LogFfmpeg
}

// GetMjpeg720pOnly returns whether MJPEG mode selection should avoid >720p.
func GetMjpeg720pOnly() bool {
	return Mjpeg720pOnly
}

// ---------------------------------------------------------------------------
// Multicast helpers
// ---------------------------------------------------------------------------

// applyDefaults fills in zero-value fields of MulticastSettings.
func (m *MulticastSettings) applyDefaults() {
	if m.IP == "" {
		m.IP = "239.255.0.1"
	}
}

func hasMulticastEnabledKey(raw map[string]interface{}) bool {
	multicastRaw, ok := raw["multicast"]
	if !ok {
		return false
	}
	section, ok := multicastRaw.(map[string]interface{})
	if !ok {
		return false
	}
	_, hasEnabled := section["enabled"]
	return hasEnabled
}

// buildCameraConfigs creates CameraConfiguration entries for each non-zero port.
func (m *MulticastSettings) buildCameraConfigs() []CameraConfiguration {
	ports := []int{m.Camera1Port, m.Camera2Port, m.Camera3Port, m.Camera4Port}
	var cameras []CameraConfiguration
	for _, port := range ports {
		if port > 0 {
			cameras = append(cameras, CameraConfiguration{
				FfmpegCamera:     fmt.Sprintf("udp://%s:%d", m.IP, port),
				Format:           "mpegts",
				InputParameters:  "",
				OutputParameters: "-c:v copy -an",
				Recode:           false,
			})
		}
	}
	return cameras
}

// UpdateMulticastConfig writes the [multicast] section to the config file.
// It replaces an existing [multicast] section or appends one.
func UpdateMulticastConfig(configFile string, settings MulticastSettings) error {
	content, err := os.ReadFile(configFile)
	if err != nil {
		return err
	}

	lines := strings.Split(string(content), "\n")

	// Find existing [multicast] section boundaries
	sectionStart := -1
	sectionEnd := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[multicast]" {
			sectionStart = i
			continue
		}
		// End of section: next top-level section header (not [[encoder]] style)
		if sectionStart >= 0 && sectionStart != i && strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[[") {
			sectionEnd = i
			break
		}
	}

	// Build replacement text
	newSection := []string{
		"[multicast]",
		fmt.Sprintf("    enabled = %v", settings.Enabled),
		fmt.Sprintf("    ip = \"%s\"", settings.IP),
		fmt.Sprintf("    camera1Port = %d", settings.Camera1Port),
		fmt.Sprintf("    camera2Port = %d", settings.Camera2Port),
		fmt.Sprintf("    camera3Port = %d", settings.Camera3Port),
		fmt.Sprintf("    camera4Port = %d", settings.Camera4Port),
	}

	var newLines []string
	if sectionStart >= 0 {
		// Replace existing section
		newLines = append(newLines, lines[:sectionStart]...)
		newLines = append(newLines, newSection...)
		newLines = append(newLines, lines[sectionEnd:]...)
	} else {
		// Insert before the first camera section ([windows...] or [linux...])
		insertAt := len(lines)
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[windows") || strings.HasPrefix(trimmed, "[linux") {
				insertAt = i
				break
			}
		}
		newLines = append(newLines, lines[:insertAt]...)
		newLines = append(newLines, "")
		newLines = append(newLines, newSection...)
		newLines = append(newLines, "")
		newLines = append(newLines, lines[insertAt:]...)
	}

	return os.WriteFile(configFile, []byte(strings.Join(newLines, "\n")), 0644)
}
