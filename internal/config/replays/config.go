package replays

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
)

// Config represents the replays configuration file structure.
type Config struct {
	Port      int                          `toml:"port"`
	VideoDir  string                       `toml:"videoDir"`
	Width     int                          `toml:"width"`
	Height    int                          `toml:"height"`
	Fps       int                          `toml:"fps"`
	OwlCMS    string                       `toml:"owlcms"`
	Platform  string                       `toml:"platform"`
	LogFfmpeg bool                         `toml:"logFfmpeg"`
	Multicast config.MulticastSettings     `toml:"multicast"`
	Cameras   []config.CameraConfiguration `toml:"-"`
}

var currentConfig *Config

// LoadConfig loads the configuration from the specified file.
func LoadConfig(configFile string) (*Config, error) {
	if config.InstallDir == "" {
		config.InstallDir = config.GetInstallDir()
	}

	if err := ExtractDefaultConfig(configFile); err != nil {
		return nil, fmt.Errorf("failed to extract default config: %w", err)
	}

	var cfg Config
	if _, err := toml.DecodeFile(configFile, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file '%s': %w\n\nPlease check the file syntax and ensure all values are properly formatted", configFile, err)
	}

	if cfg.VideoDir == "" {
		cfg.VideoDir = "videos"
	}
	if !filepath.IsAbs(cfg.VideoDir) {
		cfg.VideoDir = filepath.Join(config.GetInstallDir(), cfg.VideoDir)
	}
	if err := os.MkdirAll(cfg.VideoDir, os.ModePerm); err != nil {
		return nil, fmt.Errorf("failed to create video directory '%s': %w", cfg.VideoDir, err)
	}
	logging.InfoLogger.Printf("Videos will be stored in: %s", cfg.VideoDir)

	var raw map[string]interface{}
	if _, err := toml.DecodeFile(configFile, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse config file for camera configurations: %w", err)
	}

	if !hasMulticastEnabledKey(raw) {
		cfg.Multicast.Enabled = true
	}

	platformKey := getPlatformName()
	var cameras []config.CameraConfiguration

	decodePlatformConfig := func(sectionName string, data interface{}) (config.CameraConfiguration, error) {
		m, ok := data.(map[string]interface{})
		if !ok {
			return config.CameraConfiguration{}, fmt.Errorf("invalid type for platform config")
		}
		var pc config.CameraConfiguration
		if val, ok := m["enabled"].(bool); ok && !val {
			logging.InfoLogger.Printf("Camera configuration for section %s is disabled", sectionName)
			return config.CameraConfiguration{}, fmt.Errorf("camera configuration for section %s is disabled", sectionName)
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
			pc.Recode = false
		}
		if pc.Format == "dshow" && !strings.HasPrefix(pc.FfmpegCamera, "video=") {
			pc.FfmpegCamera = "video=" + pc.FfmpegCamera
		}
		return pc, nil
	}

	loadPlatformCamerasFromRaw := func(rawMap map[string]interface{}, sourceName string) []config.CameraConfiguration {
		var result []config.CameraConfiguration
		for i := 1; ; i++ {
			key := platformKey
			if i > 1 {
				key = platformKey + strconv.Itoa(i)
			}
			confRaw, exists := rawMap[key]
			if !exists {
				if platformKey == "windows" && i == 1 {
					confRaw, exists = rawMap["windows1"]
				} else if platformKey == "linux" && i == 1 {
					confRaw, exists = rawMap["linux1"]
				}
				if !exists {
					break
				}
			}
			pc, err := decodePlatformConfig(key, confRaw)
			if err != nil {
				continue
			}
			result = append(result, pc)
		}
		if len(result) > 0 {
			logging.InfoLogger.Printf("Loaded %d camera configurations from %s", len(result), sourceName)
		}
		return result
	}

	if cfg.Multicast.Enabled {
		multicastPath := filepath.Join(config.GetInstallDir(), "multicast.toml")
		if err := ExtractDefaultMulticastConfig(multicastPath); err != nil {
			return nil, fmt.Errorf("failed to extract default multicast config: %w", err)
		}
		multicastSettings, err := LoadMulticastConfig(multicastPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load multicast config '%s': %w", multicastPath, err)
		}
		cfg.Multicast.IP = multicastSettings.IP
		cfg.Multicast.Camera1Port = multicastSettings.Camera1Port
		cfg.Multicast.Camera2Port = multicastSettings.Camera2Port
		cfg.Multicast.Camera3Port = multicastSettings.Camera3Port
		cfg.Multicast.Camera4Port = multicastSettings.Camera4Port

		multicastCameras := cfg.Multicast.BuildCameraConfigs()
		if len(multicastCameras) == 0 {
			return nil, fmt.Errorf("multicast is enabled but no camera ports are configured in %s", multicastPath)
		}
		cameras = multicastCameras
		logging.InfoLogger.Printf("Multicast mode enabled: loaded %d multicast camera(s) from %s", len(multicastCameras), multicastPath)
	} else {
		autoTomlPath := filepath.Join(config.GetInstallDir(), "auto.toml")
		if _, err := os.Stat(autoTomlPath); err == nil {
			logging.InfoLogger.Printf("Found auto.toml, loading camera configurations from it")
			var autoRaw map[string]interface{}
			if _, err := toml.DecodeFile(autoTomlPath, &autoRaw); err == nil {
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

		if len(cameras) == 0 {
			defaultCamerasPath := filepath.Join(config.GetInstallDir(), "default_cameras.toml")
			if err := ExtractDefaultPlatformCamerasConfig(config.GetInstallDir()); err != nil {
				return nil, fmt.Errorf("failed to extract default cameras config: %w", err)
			}
			var defaultRaw map[string]interface{}
			if _, err := toml.DecodeFile(defaultCamerasPath, &defaultRaw); err != nil {
				logging.ErrorLogger.Printf("Failed to parse default_cameras.toml: %v", err)
			} else {
				cameras = loadPlatformCamerasFromRaw(defaultRaw, defaultCamerasPath)
			}
		}

		if len(cameras) == 0 {
			cameras = loadPlatformCamerasFromRaw(raw, configFile)
		}
	}

	cfg.Cameras = cameras
	cfg.Multicast.ApplyDefaults()

	config.SetVideoDir(cfg.VideoDir)
	config.SetVideoConfig(cfg.Width, cfg.Height, cfg.Fps)

	platformKey = getPlatformName()
	logging.InfoLogger.Printf("Configuration loaded from %s for platform %s:\n"+
		"    Port: %d\n"+
		"    VideoDir: %s\n",
		configFile, platformKey, cfg.Port, cfg.VideoDir)

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
			camera.FfmpegPath, camera.FfmpegCamera, camera.Format,
			camera.Params, camera.Size, camera.Fps, camera.Recode)
	}

	currentConfig = &cfg
	config.LogFfmpeg = cfg.LogFfmpeg
	return &cfg, nil
}

// GetCurrentConfig returns the current configuration.
func GetCurrentConfig() *Config {
	return currentConfig
}

// ValidateCamera checks if camera configuration is correct for the platform.
func (c *Config) ValidateCamera() error {
	if len(c.Cameras) == 0 || c.Cameras[0].FfmpegCamera == "" {
		return fmt.Errorf("camera not configured")
	}
	return nil
}

// InitConfig processes command-line flags and loads the configuration.
func InitConfig() (*Config, error) {
	config.AppName = "replays"

	configFile := flag.String("config", "", "path to configuration file")
	flag.StringVar(&config.ConfigDir, "configDir", "",
		"directory containing editable config files (config.toml, auto.toml, multicast.toml, etc.)")
	flag.StringVar(&config.InstallDir, "dir", "replays", fmt.Sprintf(
		`Name of an alternate installation directory. Default is 'replays'.
Value is relative to the platform-specific directory for applcation data (%s)
Used for multiple installations on the same machine (e.g. 'replays2, replay3').
An absolute path can be provded if needed.`, config.GetInstallDir()))
	verbose := flag.Bool("v", false, "enable verbose logging")
	verboseAlt := flag.Bool("verbose", false, "enable verbose logging")
	flag.BoolVar(&config.NoVideo, "noVideo", false, "log ffmpeg actions but do not execute them")
	flag.BoolVar(&config.NoMQTT, "noMQTT", false, "disable MQTT autodiscovery and monitoring")
	flag.StringVar(&config.AutoTomlDir, "autoTomlDir", "",
		"directory for auto.toml output (default: install dir)")
	flag.Parse()

	if err := config.ResolveAndEnsureConfigDir(); err != nil {
		return nil, err
	}

	if *configFile == "" {
		*configFile = filepath.Join(config.GetInstallDir(), "config.toml")
	}

	logging.SetVerbose(*verbose || *verboseAlt)
	logDir := filepath.Join(config.GetRuntimeDir(), "logs")
	if err := logging.Init(logDir); err != nil {
		return nil, fmt.Errorf("failed to initialize logging: %w", err)
	}

	cfg, err := LoadConfig(*configFile)
	if err != nil {
		return nil, fmt.Errorf("error loading configuration: %w", err)
	}

	config.SetCameraConfigs(cfg.Cameras)
	return cfg, nil
}

// UpdateConfigFile updates the owlcms address in the config file.
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

// UpdatePlatform updates the platform value in the config file.
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

// UpdateMulticastConfig writes the [multicast] section to the config file.
func UpdateMulticastConfig(configFile string, settings config.MulticastSettings) error {
	content, err := os.ReadFile(configFile)
	if err != nil {
		return err
	}

	lines := strings.Split(string(content), "\n")
	sectionStart := -1
	sectionEnd := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[multicast]" {
			sectionStart = i
			continue
		}
		if sectionStart >= 0 && sectionStart != i &&
			strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[[") {
			sectionEnd = i
			break
		}
	}

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
		newLines = append(newLines, lines[:sectionStart]...)
		newLines = append(newLines, newSection...)
		newLines = append(newLines, lines[sectionEnd:]...)
	} else {
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

// ---------------------------------------------------------------------------
// Private helpers
// ---------------------------------------------------------------------------

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

func getPlatformName() string {
	if isWSL() {
		return "WSL"
	}
	return runtime.GOOS
}
