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

// on Windows, we use the locally downloaded ffmpeg
func findFFmpeg() string {
	installDir := config.GetInstallDir()
	ffmpegPath := filepath.Join(installDir, FfmpegBuild, "bin", "ffmpeg.exe")
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

func forceKillCmd(cmd *exec.Cmd) error {
	logging.InfoLogger.Printf("Killing ffmpeg process %d", cmd.Process.Pid)
	if cmd.Process == nil {
		return nil
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	return kill.Run()
}
