package recording

import (
	"os/exec"
	"syscall"

	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
)

// createFfmpegCmd creates an exec.Cmd for ffmpeg
func createFfmpegCmd(args []string) *exec.Cmd {
	cameras := config.GetCameraConfigs()
	path := "ffmpeg"
	if len(cameras) > 0 {
		path = cameras[0].FfmpegPath
	}

	// If no path configured, try to find ffmpeg in PATH
	if path == "" {
		var err error
		path, err = exec.LookPath("ffmpeg")
		if err != nil {
			logging.ErrorLogger.Printf("No ffmpeg path configured and ffmpeg not found in PATH: %v", err)
			// Use default name, will fail if not in current directory
			path = "ffmpeg"
		}
	}

	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	return cmd
}

func forceKillCmd(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// Kill the entire process group
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return err
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}
