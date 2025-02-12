//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly || solaris

package recording

import (
	"os/exec"
)

// createFfmpegCmd creates an exec.Cmd for ffmpeg (non-Windows version)
func createFfmpegCmd(args []string) *exec.Cmd {
	path := FfmpegPath
	if len(CameraConfigs) > 0 {
		path = CameraConfigs[0].FfmpegPath
	}
	return exec.Command(path, args...)
}
