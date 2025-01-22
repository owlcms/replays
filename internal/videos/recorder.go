package videos

import (
	"fmt"
	"os"
	"os/exec"

	"path/filepath"
	"strconv"
	"time"

	"github.com/owlcms/replays/internal/logging"
)

var currentRecording *exec.Cmd
var currentFileName string
var startTimeMillis string
var noVideo bool
var videoDir string

// SetNoVideo sets the noVideo flag
func SetNoVideo(value bool) {
	noVideo = value
}

// SetVideoDir sets the video directory
func SetVideoDir(dir string) {
	videoDir = dir
}

// StartRecording starts recording a video using ffmpeg
func StartRecording(fullName, liftTypeKey string, attemptNumber int, startMillis string) error {
	fileName := filepath.Join(videoDir, fmt.Sprintf("%s_%s_attempt%d_%s.mp4", fullName, liftTypeKey, attemptNumber, startMillis))
	quotedFileName := fmt.Sprintf("\"%s\"", fileName)

	// If there is an ongoing recording, stop it and discard the file
	if currentRecording != nil {
		if err := currentRecording.Process.Kill(); err != nil {
			return fmt.Errorf("failed to stop previous recording: %w", err)
		}
		if err := os.Remove(currentFileName); err != nil {
			return fmt.Errorf("failed to remove previous recording file: %w", err)
		}
		logging.InfoLogger.Printf("Stopped and removed previous recording: %s", currentFileName)
	}

	cmd := exec.Command("ffmpeg", "-y", "-f", "v4l2", "-i", "/dev/video0", quotedFileName)

	if noVideo {
		logging.InfoLogger.Printf("Simulating start recording video: %s", fileName)
		logging.InfoLogger.Printf("ffmpeg command: %s", cmd.String())
		currentFileName = fileName
		startTimeMillis = startMillis
		return nil
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	currentRecording = cmd
	currentFileName = fileName
	startTimeMillis = startMillis

	logging.InfoLogger.Printf("Started recording video: %s", fileName)
	return nil
}

// StopRecording stops the current recording and trims the video
func StopRecording(_ string) error {
	if currentRecording == nil && !noVideo {
		return fmt.Errorf("no ongoing recording to stop")
	}

	if noVideo {
		logging.InfoLogger.Printf("Simulating stop recording video: %s", currentFileName)
	} else {
		// Stop the recording
		logging.InfoLogger.Println("Sending signal to ffmpeg to stop recording")
		if err := currentRecording.Process.Kill(); err != nil {
			return fmt.Errorf("failed to stop recording: %w", err)
		}
	}

	// Compute the difference between start time and stop time, subtract 5 seconds
	startTime, err := strconv.ParseInt(startTimeMillis, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid start time: %w", err)
	}
	stopTime := time.Now().UnixNano() / int64(time.Millisecond)
	duration := stopTime - startTime - 5000 // subtract 5 seconds

	logging.InfoLogger.Printf("Duration to be trimmed: %d milliseconds", duration)

	// Save the video with an ISO 8601 timestamp without time zone
	timestamp := time.Now().Format("2006-01-02_15h04m05s")
	finalFileName := filepath.Join(videoDir, fmt.Sprintf("%s_%s", timestamp, filepath.Base(currentFileName)))
	quotedFinalFileName := fmt.Sprintf("\"%s\"", finalFileName)
	quotedCurrentFileName := fmt.Sprintf("\"%s\"", currentFileName)

	if startTime == 0 {
		logging.InfoLogger.Println("Start time is 0, not trimming the video")
		if noVideo {
			logging.InfoLogger.Printf("Simulating rename video: %s -> %s", currentFileName, finalFileName)
		} else if err := os.Rename(currentFileName, finalFileName); err != nil {
			return fmt.Errorf("failed to rename video file to %s: %w", finalFileName, err)
		}
	} else {
		cmd := exec.Command("ffmpeg", "-y", "-ss", fmt.Sprintf("%d", duration/1000), "-i", quotedCurrentFileName, "-c", "copy", quotedFinalFileName)

		if noVideo {
			logging.InfoLogger.Printf("Simulating trim video: %s", finalFileName)
			logging.InfoLogger.Printf("ffmpeg command: %s", cmd.String())
		} else {
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to trim video: %w", err)
			}
			if err := os.Remove(currentFileName); err != nil {
				return fmt.Errorf("failed to remove untrimmed video file: %w", err)
			}
		}
	}

	logging.InfoLogger.Printf("Stopped recording and saved video: %s", finalFileName)
	currentRecording = nil
	currentFileName = ""

	return nil
}

// GetStartTimeMillis returns the start time in milliseconds
func GetStartTimeMillis() string {
	return startTimeMillis
}
