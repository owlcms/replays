//go:build windows

package recording

import (
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/owlcms/replays/internal/logging"
	"golang.org/x/sys/windows"
)

func createFfmpegCmd(args []string) *exec.Cmd {
	ffmpegPath := FfmpegPath
	if !filepath.IsAbs(ffmpegPath) {
		var err error
		ffmpegPath, err = exec.LookPath(FfmpegPath)
		if err != nil {
			logging.ErrorLogger.Fatalf("ffmpeg executable not found in PATH: %v", err)
		}
	}

	cmd := exec.Command(ffmpegPath, args...)
	// Hide the command window on Windows
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	return cmd
}
