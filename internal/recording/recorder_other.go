//go:build !windows

package recording

import "os/exec"

func createFfmpegCmd(args []string) *exec.Cmd {
	return exec.Command(FfmpegPath, args...)
}
