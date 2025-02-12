//go:build windows && !darwin && linux

package recording

import (
	"os/exec"

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

	return exec.Command(path, args...)
}
