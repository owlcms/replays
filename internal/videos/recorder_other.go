//go:build !windows

package videos

import "os/exec"

func createFfmpegCmd(args []string) *exec.Cmd {
	return exec.Command(FfmpegPath, args...)
}
