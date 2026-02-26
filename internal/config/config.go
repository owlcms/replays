package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/owlcms/replays/internal/logging"
)

// CameraConfiguration represents platform-specific configurations.
type CameraConfiguration struct {
	FfmpegPath       string `toml:"ffmpegPath"`
	FfmpegCamera     string `toml:"ffmpegCamera"`
	Platform         string `toml:"platform"`
	Format           string `toml:"format"`
	Params           string `toml:"params"`
	InputParameters  string `toml:"inputParameters"`
	OutputParameters string `toml:"outputParameters"`
	Size             string `toml:"size"`
	Fps              int    `toml:"fps"`
	Recode           bool   `toml:"recode"`
}

// MulticastSettings holds the multicast camera configuration.
type MulticastSettings struct {
	Enabled     bool   `toml:"enabled"`
	IP          string `toml:"ip"`
	Camera1Port int    `toml:"camera1Port"`
	Camera2Port int    `toml:"camera2Port"`
	Camera3Port int    `toml:"camera3Port"`
	Camera4Port int    `toml:"camera4Port"`
}

var (
	Verbose       bool
	NoVideo       bool
	NoMQTT        bool
	AutoTomlDir   string
	ConfigDir     string // per-instance config dir (set by --configDir)
	InstallDir    string
	AppName       string // "cameras" or "replays" — set by each binary before config resolution
	videoDir      string
	Width         int
	Height        int
	Fps           int
	Recode        bool
	LogFfmpeg     bool
	Mjpeg720pOnly = IsLinuxARM()
	CameraConfigs []CameraConfiguration
	ffmpegPath    string
)

const ControlPanelDirEnv = "VIDEO_CONTROLPANEL_DIR"
const SharedConfigDirEnv = "VIDEO_CONFIGDIR"
const LocalVideoConfigDir = "video_config"

// ResolveAndEnsureConfigDir resolves the per-instance ConfigDir:
// 1) explicit --configDir value (already stored in ConfigDir),
// 2) local ./video_config/<AppName> fallback for development.
// VIDEO_CONFIGDIR is NOT used here — it is the shared directory for ffmpeg.toml only.
// It normalizes to an absolute path and ensures the directory exists.
func ResolveAndEnsureConfigDir() error {
	if strings.TrimSpace(ConfigDir) == "" {
		if AppName == "" {
			ConfigDir = filepath.Join(".", LocalVideoConfigDir)
		} else {
			ConfigDir = filepath.Join(".", LocalVideoConfigDir, AppName)
		}
	}

	absConfigDir, err := filepath.Abs(ConfigDir)
	if err != nil {
		return fmt.Errorf("invalid configDir '%s': %w", ConfigDir, err)
	}
	ConfigDir = absConfigDir

	if err := os.MkdirAll(ConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create configDir '%s': %w", ConfigDir, err)
	}

	return nil
}

// IsLocalDevRuntime reports whether the runtime root is the default local
// ./video_config/<AppName> folder (i.e. not overridden by --configDir and
// not launched by the control panel with VIDEO_CONFIGDIR).
func IsLocalDevRuntime() bool {
	if strings.TrimSpace(os.Getenv(SharedConfigDirEnv)) != "" {
		return false
	}

	var devDir string
	if AppName == "" {
		devDir = filepath.Join(".", LocalVideoConfigDir)
	} else {
		devDir = filepath.Join(".", LocalVideoConfigDir, AppName)
	}
	absDevDir, err := filepath.Abs(devDir)
	if err != nil {
		return false
	}

	return filepath.Clean(GetInstallDir()) == filepath.Clean(absDevDir)
}

// GetInstallDir returns the per-instance configuration directory.
// This is where app-specific config files (config.toml, auto.toml, etc.) live.
// It does NOT return the shared directory — use GetSharedConfigDir() for that.
func GetInstallDir() string {
	if ConfigDir != "" {
		return ConfigDir
	}

	var fallback string
	if AppName == "" {
		fallback = filepath.Join(".", LocalVideoConfigDir)
	} else {
		fallback = filepath.Join(".", LocalVideoConfigDir, AppName)
	}
	if abs, err := filepath.Abs(fallback); err == nil {
		return abs
	}
	return fallback
}

// GetSharedConfigDir returns the shared configuration directory.
// This is where ffmpeg.toml lives. In control-panel mode it comes from
// VIDEO_CONFIGDIR; in dev mode it falls back to ./video_config/ffmpeg.
func GetSharedConfigDir() string {
	if envDir := strings.TrimSpace(os.Getenv(SharedConfigDirEnv)); envDir != "" {
		if abs, err := filepath.Abs(envDir); err == nil {
			return abs
		}
		return envDir
	}
	if abs, err := filepath.Abs(filepath.Join(".", LocalVideoConfigDir, "ffmpeg")); err == nil {
		return abs
	}
	return filepath.Join(".", LocalVideoConfigDir, "ffmpeg")
}

// GetRuntimeDir returns the directory of the running executable.
func GetRuntimeDir() string {
	if exePath, err := os.Executable(); err == nil {
		if absExePath, absErr := filepath.Abs(exePath); absErr == nil {
			return filepath.Dir(absExePath)
		}
		return filepath.Dir(exePath)
	}

	if wd, err := os.Getwd(); err == nil {
		return wd
	}

	return GetInstallDir()
}

// GetControlPanelInstallDir returns the shared control panel installation directory.
func GetControlPanelInstallDir() string {
	if envDir := strings.TrimSpace(os.Getenv(ControlPanelDirEnv)); envDir != "" {
		if absEnvDir, err := filepath.Abs(envDir); err == nil {
			return absEnvDir
		}
		return envDir
	}

	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "owlcms-controlpanel")
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "owlcms-controlpanel")
	case "linux":
		return filepath.Join(os.Getenv("HOME"), ".local", "share", "owlcms-controlpanel")
	default:
		return "./owlcms-controlpanel"
	}
}

// GetFFmpegBootstrapDir returns where FFmpeg runtime archives should be
// downloaded/extracted.
func GetFFmpegBootstrapDir() string {
	if envDir := strings.TrimSpace(os.Getenv(ControlPanelDirEnv)); envDir != "" {
		if absEnvDir, err := filepath.Abs(envDir); err == nil {
			return filepath.Join(absEnvDir, "ffmpeg")
		}
		return filepath.Join(envDir, "ffmpeg")
	}

	if absLocalVideo, err := filepath.Abs(filepath.Join(".", LocalVideoConfigDir, "ffmpeg")); err == nil {
		return absLocalVideo
	}
	return filepath.Join(".", LocalVideoConfigDir, "ffmpeg")
}

// GetSharedFFmpegRootDirs returns candidate shared FFmpeg root directories
// managed by Control Panel.
func GetSharedFFmpegRootDirs() []string {
	roots := make([]string, 0, 8)
	seen := make(map[string]struct{})

	add := func(path string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		clean := filepath.Clean(path)
		if _, ok := seen[clean]; ok {
			return
		}
		seen[clean] = struct{}{}
		roots = append(roots, clean)
	}

	if ffmpegPath := strings.TrimSpace(os.Getenv("VIDEO_FFMPEG_PATH")); ffmpegPath != "" {
		ffmpegDir := filepath.Dir(ffmpegPath)
		if strings.EqualFold(filepath.Base(ffmpegDir), "bin") {
			add(filepath.Dir(ffmpegDir))
		}
		add(ffmpegDir)
	}

	sharedBase := filepath.Join(GetControlPanelInstallDir(), "ffmpeg")
	entries, err := os.ReadDir(sharedBase)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				add(filepath.Join(sharedBase, entry.Name()))
			}
		}
	}

	sort.SliceStable(roots, func(i, j int) bool {
		return roots[i] > roots[j]
	})

	return roots
}

// FindSharedFFmpegExecutable resolves an executable name from shared Control
// Panel FFmpeg directories. Returns empty string when not found.
func FindSharedFFmpegExecutable(executableName string) string {
	for _, root := range GetSharedFFmpegRootDirs() {
		binCandidate := filepath.Join(root, "bin", executableName)
		if _, err := os.Stat(binCandidate); err == nil {
			return binCandidate
		}

		directCandidate := filepath.Join(root, executableName)
		if _, err := os.Stat(directCandidate); err == nil {
			return directCandidate
		}
	}

	return ""
}

// IsLinuxARM reports whether the process is running on Linux ARM/ARM64.
func IsLinuxARM() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	return runtime.GOARCH == "arm" || runtime.GOARCH == "arm64"
}

// ---------------------------------------------------------------------------
// Getters / setters for shared state
// ---------------------------------------------------------------------------

func SetCameraConfigs(configs []CameraConfiguration) {
	CameraConfigs = configs
}

func GetCameraConfigs() []CameraConfiguration {
	return CameraConfigs
}

func SetVideoDir(dir string) {
	videoDir = dir
}

func GetVideoDir() string {
	return videoDir
}

func SetVideoConfig(width, height, fps int) {
	Width = width
	Height = height
	Fps = fps
}

func SetNoVideo(noVideo bool) {
	NoVideo = noVideo
}

func SetRecode(recode bool) {
	Recode = recode
}

func SetFFmpegPath(path string) {
	ffmpegPath = path
	logging.InfoLogger.Printf("FFmpeg path set to: %s", path)
}

func GetFFmpegPath() string {
	return ffmpegPath
}

func GetLogFfmpeg() bool {
	return LogFfmpeg
}

func GetMjpeg720pOnly() bool {
	return Mjpeg720pOnly
}

// ---------------------------------------------------------------------------
// MulticastSettings helpers
// ---------------------------------------------------------------------------

// ApplyDefaults fills in zero-value fields of MulticastSettings.
func (m *MulticastSettings) ApplyDefaults() {
	if m.IP == "" {
		m.IP = "239.255.0.1"
	}
}

// BuildCameraConfigs creates CameraConfiguration entries for each non-zero port.
func (m *MulticastSettings) BuildCameraConfigs() []CameraConfiguration {
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
