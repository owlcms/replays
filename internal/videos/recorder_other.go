//go:build !windows

package videos

import "os/exec"

func createWindowsFfmpegCmd(args []string) *exec.Cmd {
	return exec.Command(FfmpegPath, args...)
}
