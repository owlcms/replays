//go:build windows

package videos

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func createWindowsFfmpegCmd(args []string) *exec.Cmd {
	cmd := exec.Command(FfmpegPath, args...)
	// Hide the command window on Windows
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	return cmd
}
