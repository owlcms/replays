//go:build !windows && !linux

package recording

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
)

// InitializeFFmpeg finds and stores the ffmpeg path in config for other platforms
func InitializeFFmpeg() error {
	path := findFFmpeg()

	// Verify the ffmpeg executable exists at the expected location
	if _, err := os.Stat(path); err != nil {
		logging.ErrorLogger.Printf("FFmpeg not found at %s: %v", path, err)
		logging.ErrorLogger.Printf("Please install FFmpeg using your package manager or from https://ffmpeg.org/")
		// Still set the path - the application will handle the error when trying to use it
		config.SetFFmpegPath(path)
		return fmt.Errorf("ffmpeg not found at expected location: %s", path)
	}

	config.SetFFmpegPath(path)
	logging.InfoLogger.Printf("FFmpeg executable set to: %s", path)
	return nil
}

// on other platforms, we try to use the system-installed ffmpeg
func findFFmpeg() string {
	// Try to find ffmpeg in PATH
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		logging.InfoLogger.Printf("Found ffmpeg in PATH at: %s", path)
		return path
	}

	// Return default path if not found
	logging.ErrorLogger.Printf("Could not find ffmpeg in PATH")
	return "ffmpeg"
}

// CreateFfmpegCmd creates an exec.Cmd for ffmpeg on other platforms
func CreateFfmpegCmd(args []string, operation string, forcedLogLevel ...string) *exec.Cmd {
	// Use the stored ffmpeg path from config
	path := config.GetFFmpegPath()

	// Handle loglevel based on logging preference or forced level
	var targetLoglevel string
	if len(forcedLogLevel) > 0 && forcedLogLevel[0] != "" {
		targetLoglevel = forcedLogLevel[0]
	} else {
		logFfmpeg := config.GetLogFfmpeg()
		targetLoglevel = "quiet"
		if logFfmpeg {
			targetLoglevel = "info"
		}
	}

	// Check if -loglevel already exists in args and update it, or add it
	foundLoglevel := false
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-loglevel" {
			args[i+1] = targetLoglevel
			foundLoglevel = true
			break
		}
	}

	// If no loglevel found, add it at the beginning
	if !foundLoglevel {
		args = append([]string{"-loglevel", targetLoglevel}, args...)
	}

	// Log the command being executed for debugging
	logging.InfoLogger.Printf("Creating ffmpeg command with path: %s", path)
	logging.InfoLogger.Printf("FFmpeg args (%d total):", len(args))
	for i, arg := range args {
		logging.InfoLogger.Printf("  [%d]: %s", i, arg)
	}

	cmd := exec.Command(path, args...)

	// Create logs directory and redirect ffmpeg output to timestamped files only if logFfmpeg is enabled
	if config.GetLogFfmpeg() {
		installDir := config.GetInstallDir()
		logsDir := filepath.Join(installDir, "logs")
		if err := os.MkdirAll(logsDir, 0755); err != nil {
			logging.ErrorLogger.Printf("Failed to create logs directory: %v", err)
		} else {
			timestamp := time.Now().Format("20060102_150405")
			logFile := filepath.Join(logsDir, fmt.Sprintf("ffmpeg_%s_%s.log", timestamp, operation))

			if file, err := os.Create(logFile); err != nil {
				logging.ErrorLogger.Printf("Failed to create ffmpeg log file %s: %v", logFile, err)
			} else {
				logging.InfoLogger.Printf("FFmpeg output will be logged to: %s", logFile)
				cmd.Stdout = file
				cmd.Stderr = file
			}
		}
	}

	return cmd
}

func forceKillCmd(cmd *exec.Cmd) error {
	logging.InfoLogger.Printf("Killing ffmpeg process %d", cmd.Process.Pid)
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
