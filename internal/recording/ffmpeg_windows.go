//go:build windows && !darwin && !linux

package recording

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
	"golang.org/x/sys/windows"
)

// InitializeFFmpeg finds and stores the ffmpeg path in config
func InitializeFFmpeg() error {
	path := findFFmpeg()
	if path == "ffmpeg.exe" || path == "ffmpeg" {
		// This is the fallback path when nothing else was found
		logging.WarningLogger.Printf("Could not find ffmpeg installation, using fallback path: %s", path)
	}
	config.SetFFmpegPath(path)
	logging.InfoLogger.Printf("FFmpeg executable set to: %s", path)
	return nil
}

// findFFmpeg finds the ffmpeg executable path based on configuration and environment
func findFFmpeg() string {
	cameras := config.GetCameraConfigs()
	path := ""

	// Try to get path from camera config
	if len(cameras) > 0 {
		path = cameras[0].FfmpegPath
	}

	// Check if the configured path exists
	if path != "" {
		// Check if it's an absolute path that exists
		if filepath.IsAbs(path) {
			if _, err := os.Stat(path); err == nil {
				logging.InfoLogger.Printf("Using configured absolute ffmpeg path: %s", path)
				return path // Path is valid and exists
			} else {
				logging.ErrorLogger.Printf("Configured absolute ffmpeg path does not exist: %s", path)
				path = "" // Reset path to try other methods
			}
		} else if !isSimpleProgramName(path) {
			// It's a relative path, check relative to current directory
			absPath := filepath.Join(".", path)
			if _, err := os.Stat(absPath); err == nil {
				logging.InfoLogger.Printf("Using configured relative ffmpeg path: %s", absPath)
				return absPath
			} else {
				logging.ErrorLogger.Printf("Configured relative ffmpeg path does not exist: %s", path)
				path = "" // Reset path to try other methods
			}
		}
	}

	// If path is a simple program name or empty, try to find it in PATH
	if path == "" || isSimpleProgramName(path) {
		programName := "ffmpeg.exe"
		if path != "" {
			programName = path
		}

		var err error
		foundPath, err := exec.LookPath(programName)
		if err == nil {
			logging.InfoLogger.Printf("Found ffmpeg in PATH: %s", foundPath)
			return foundPath
		} else {
			logging.ErrorLogger.Printf("ffmpeg not found in PATH: %v", err)
		}
	}

	// As last resort, check the installation directory
	installDir := config.GetInstallDir()
	ffmpegPath := filepath.Join(installDir, "ffmpeg-7.1-full_build", "bin", "ffmpeg.exe")
	logging.InfoLogger.Printf("Trying ffmpeg at installation directory: %s", ffmpegPath)

	if _, err := os.Stat(ffmpegPath); err == nil {
		return ffmpegPath
	} else {
		// If all else fails, use default name (will likely fail if not in current dir)
		logging.ErrorLogger.Printf("Could not find ffmpeg at installation directory, falling back to ffmpeg.exe")
		return "ffmpeg.exe"
	}
}

// createFfmpegCmd creates an exec.Cmd for ffmpeg with Windows-specific process attributes
func createFfmpegCmd(args []string) *exec.Cmd {
	// Use the stored ffmpeg path from config
	path := config.GetFFmpegPath()

	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
	}

	return cmd
}

// isSimpleProgramName checks if the path is just a program name without directory separators
func isSimpleProgramName(path string) bool {
	return filepath.Base(path) == path
}

func forceKillCmd(cmd *exec.Cmd) error {
	logging.InfoLogger.Printf("Killing ffmpeg process %d", cmd.Process.Pid)
	if cmd.Process == nil {
		return nil
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	return kill.Run()
}
