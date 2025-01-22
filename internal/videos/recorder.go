package videos

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"time"
)

var currentRecording *exec.Cmd
var currentFileName string
var startTimeMillis string
var noVideo bool

// SetNoVideo sets the noVideo flag
func SetNoVideo(value bool) {
	noVideo = value
}

// StartRecording starts recording a video using ffmpeg
func StartRecording(fullName, liftTypeKey string, attemptNumber int, startMillis string) error {
	fileName := fmt.Sprintf("%s_%s_attempt%d_%s.mp4", fullName, liftTypeKey, attemptNumber, startMillis)

	// If there is an ongoing recording, stop it and discard the file
	if currentRecording != nil {
		if err := currentRecording.Process.Kill(); err != nil {
			return fmt.Errorf("failed to stop previous recording: %w", err)
		}
		if err := os.Remove(currentFileName); err != nil {
			return fmt.Errorf("failed to remove previous recording file: %w", err)
		}
		log.Printf("Stopped and removed previous recording: %s", currentFileName)
	}

	if noVideo {
		log.Printf("Simulating start recording video: %s", fileName)
		currentFileName = fileName
		startTimeMillis = startMillis
		return nil
	}

	cmd := exec.Command("ffmpeg", "-y", "-f", "v4l2", "-i", "/dev/video0", fileName)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	currentRecording = cmd
	currentFileName = fileName
	startTimeMillis = startMillis

	log.Printf("Started recording video: %s", fileName)
	return nil
}

// StopRecording stops the current recording and trims the video
func StopRecording(stopMillis string) error {
	if currentRecording == nil && !noVideo {
		return fmt.Errorf("no ongoing recording to stop")
	}

	if noVideo {
		log.Printf("Simulating stop recording video: %s", currentFileName)
		currentFileName = ""
		return nil
	}

	// Stop the recording
	if err := currentRecording.Process.Kill(); err != nil {
		return fmt.Errorf("failed to stop recording: %w", err)
	}

	// Compute the difference between start time and stop time, subtract 5 seconds
	startTime, err := strconv.ParseInt(startTimeMillis, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid start time: %w", err)
	}
	stopTime, err := strconv.ParseInt(stopMillis, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid stop time: %w", err)
	}
	duration := stopTime - startTime - 5000 // subtract 5 seconds

	// Trim the front of the video
	trimmedFileName := fmt.Sprintf("%s_trimmed.mp4", currentFileName)
	cmd := exec.Command("ffmpeg", "-y", "-ss", fmt.Sprintf("%d", duration/1000), "-i", currentFileName, "-c", "copy", trimmedFileName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to trim video: %w", err)
	}

	// Rename the trimmed file with an ISO 8601 timestamp
	timestamp := time.Now().Format(time.RFC3339)
	finalFileName := fmt.Sprintf("%s_%s", timestamp, currentFileName)
	if err := os.Rename(trimmedFileName, finalFileName); err != nil {
		return fmt.Errorf("failed to rename video file: %w", err)
	}

	log.Printf("Stopped recording and saved video: %s", finalFileName)
	currentRecording = nil
	currentFileName = ""

	return nil
}
