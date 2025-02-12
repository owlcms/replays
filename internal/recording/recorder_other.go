//go:build linux || freebsd || openbsd || netbsd || dragonfly || solaris || darwin

package recording

import (
	"os/exec"

	"github.com/owlcms/replays/internal/logging"
)

// createFfmpegCmd creates an exec.Cmd for ffmpeg (non-Windows version)
func createFfmpegCmd(args []string) *exec.Cmd {
	path := FfmpegPath
	if len(CameraConfigs) > 0 {
		path = CameraConfigs[0].FfmpegPath
	}

	cmd := exec.Command(path, args...)

	// Log the command and arguments
	logging.InfoLogger.Printf("Executing command: %s %s", path, args)

	return cmd
}
