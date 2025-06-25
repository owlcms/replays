//go:build windows && !darwin && !linux

package recording

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
	"golang.org/x/sys/windows"
)

// InitializeFFmpeg finds and stores the ffmpeg path in config
func InitializeFFmpeg() error {
	path := findFFmpeg()

	// Verify the ffmpeg executable exists at the expected location
	if _, err := os.Stat(path); err != nil {
		logging.ErrorLogger.Printf("FFmpeg not found at %s: %v", path, err)
		logging.ErrorLogger.Printf("Please ensure FFmpeg is properly downloaded to the installation directory")
		// Still set the path - the application will handle the error when trying to use it
		config.SetFFmpegPath(path)
		return fmt.Errorf("ffmpeg not found at expected location: %s", path)
	}

	config.SetFFmpegPath(path)
	logging.InfoLogger.Printf("FFmpeg executable set to: %s", path)
	return nil
}

// on Windows, we use the locally downloaded ffmpeg
func findFFmpeg() string {
	installDir := config.GetInstallDir()
	ffmpegPath := filepath.Join(installDir, FfmpegBuild, "bin", "ffmpeg.exe")
	logging.InfoLogger.Printf("Trying ffmpeg at installation directory: %s", ffmpegPath)

	if _, err := os.Stat(ffmpegPath); err == nil {
		logging.InfoLogger.Printf("Found ffmpeg at: %s", ffmpegPath)
		return ffmpegPath
	} else {
		logging.ErrorLogger.Printf("Could not find ffmpeg at expected location %s: %v", ffmpegPath, err)

		// Try to check if the directory structure exists
		binDir := filepath.Join(installDir, FfmpegBuild, "bin")
		if entries, err := os.ReadDir(binDir); err == nil {
			logging.InfoLogger.Printf("Contents of %s:", binDir)
			for _, entry := range entries {
				logging.InfoLogger.Printf("  - %s", entry.Name())
			}
		} else {
			logging.ErrorLogger.Printf("Could not read ffmpeg bin directory %s: %v", binDir, err)
		}

		// Return the expected path even if not found - the error will be handled upstream
		// This ensures we never try to use ffmpeg from PATH
		return ffmpegPath
	}
}

// CreateFfmpegCmd creates an exec.Cmd for ffmpeg with Windows-specific process attributes
func CreateFfmpegCmd(args []string, operation string) *exec.Cmd {
	// Use the stored ffmpeg path from config
	path := config.GetFFmpegPath()

	// Handle loglevel based on logging preference
	logFfmpeg := config.GetLogFfmpeg()
	targetLoglevel := "quiet"
	if logFfmpeg {
		targetLoglevel = "info"
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
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
	}

	// Create logs directory and redirect ffmpeg output to timestamped files only if logFfmpeg is enabled
	if logFfmpeg {
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
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	return kill.Run()
}
