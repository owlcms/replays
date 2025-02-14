//go:build windows && !darwin && !linux

package recording

import (
	"os/exec"
	"strconv"
	"syscall"

	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
	"golang.org/x/sys/windows"
)

// createFfmpegCmd creates an exec.Cmd for ffmpeg with Windows-specific process attributes
func createFfmpegCmd(args []string) *exec.Cmd {
	cameras := config.GetCameraConfigs()
	path := "ffmpeg.exe"
	if len(cameras) > 0 {
		path = cameras[0].FfmpegPath
	}

	// If no path configured, try to find ffmpeg.exe in PATH
	if path == "" {
		var err error
		path, err = exec.LookPath("ffmpeg.exe")
		if err != nil {
			logging.ErrorLogger.Printf("No ffmpeg path configured and ffmpeg.exe not found in PATH: %v", err)
			// Use default name, will fail if not in current directory
			path = "ffmpeg.exe"
		}
	}

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
