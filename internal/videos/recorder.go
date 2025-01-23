package videos

import (
	"fmt"
	"os"
	"os/exec"

	"path/filepath"
	"strconv"
	"time"

	"strings"

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
	// Ensure the video directory exists
	if err := os.MkdirAll(videoDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create video directory: %w", err)
	}

	// Replace blanks in fullName with underscores
	fullName = strings.ReplaceAll(fullName, " ", "_")

	fileName := filepath.Join(videoDir, fmt.Sprintf("%s_%s_attempt%d_%s.mp4", fullName, liftTypeKey, attemptNumber, startMillis))

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

	var cmd *exec.Cmd
	if noVideo {
		cmd = exec.Command("ffmpeg", "-y", "-f", "v4l2", "-i", "/dev/video0", fileName)
		logging.InfoLogger.Printf("Simulating start recording video: %s", fileName)
	} else {
		cmd = exec.Command("ffmpeg", "-y", "-f", "v4l2", "-i", "/dev/video0", fileName)
		logging.InfoLogger.Printf("%s", cmd.String())
	}

	if noVideo {
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
		if err := currentRecording.Process.Signal(os.Interrupt); err != nil {
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
	baseFileName := strings.TrimSuffix(filepath.Base(currentFileName), filepath.Ext(currentFileName))
	finalFileName := filepath.Join(videoDir, fmt.Sprintf("%s_%s.mp4", timestamp, baseFileName))

	if startTime == 0 {
		logging.InfoLogger.Println("Start time is 0, not trimming the video")
		if noVideo {
			logging.InfoLogger.Printf("Simulating rename video: %s -> %s", currentFileName, finalFileName)
		} else if err := os.Rename(currentFileName, finalFileName); err != nil {
			return fmt.Errorf("failed to rename video file to %s: %w", finalFileName, err)
		}
	} else {
		for i := 0; i < 5; i++ {
			cmd := exec.Command("ffmpeg", "-y", "-ss", fmt.Sprintf("%d", duration/1000), "-i", currentFileName, "-c", "copy", finalFileName)
			logging.InfoLogger.Printf("%s", cmd.String())

			if err := cmd.Run(); err != nil {
				logging.ErrorLogger.Printf("Failed to trim video (attempt %d/5): %v", i+1, err)
				logging.ErrorLogger.Printf("ffmpeg command: %s", cmd.String())
				time.Sleep(1 * time.Second)
			} else {
				break
			}
			if i == 4 {
				return fmt.Errorf("failed to trim video after 5 attempts: %w", err)
			}
		}
		if err := os.Remove(currentFileName); err != nil {
			return fmt.Errorf("failed to remove untrimmed video file: %w", err)
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
