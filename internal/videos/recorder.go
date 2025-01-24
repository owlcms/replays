package videos

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/state"
	"github.com/owlcms/replays/internal/status"
)

var (
	currentRecording *exec.Cmd
	currentStdin     *os.File
	currentFileName  string
)

// Remove the duplicated SendStatus function

// StartRecording starts recording a video using ffmpeg
func StartRecording(fullName, liftTypeKey string, attemptNumber int) error {
	// Update state
	state.CurrentAthlete = fullName
	state.CurrentLiftType = liftTypeKey
	state.CurrentAttempt = attemptNumber

	// Ensure the video directory exists
	if err := os.MkdirAll(VideoDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create video directory: %w", err)
	}

	// Replace blanks in fullName with underscores
	fullName = strings.ReplaceAll(fullName, " ", "_")

	fileName := filepath.Join(VideoDir, fmt.Sprintf("%s_%s_attempt%d_%d.mp4", fullName, liftTypeKey, attemptNumber, state.LastStartTime))

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
	args := []string{
		"-y",               // Overwrite output files
		"-f", FfmpegFormat, // Format
		"-video_size", fmt.Sprintf("%dx%d", Width, Height),
		"-framerate", fmt.Sprintf("%d", Fps),
		"-i", FfmpegCamera,
		fileName,
	}

	if NoVideo {
		cmd = exec.Command(FfmpegPath, args...)
		logging.InfoLogger.Printf("Simulating start recording video: %s", fileName)
	} else {
		cmd = exec.Command(FfmpegPath, args...)
		logging.InfoLogger.Printf("Executing command: %s %s", FfmpegPath, strings.Join(args, " "))
	}

	if NoVideo {
		logging.InfoLogger.Printf("ffmpeg command: %s", cmd.String())
		currentFileName = fileName
		state.LastTimerStopTime = 0
		return nil
	}

	// Create a pipe for stdin
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	currentRecording = cmd
	currentStdin = stdin.(*os.File)
	currentFileName = fileName
	state.LastTimerStopTime = 0

	// Update status with spaces instead of underscores
	SendStatus(status.Recording, fmt.Sprintf("Recording: %s - %s attempt %d",
		strings.ReplaceAll(fullName, "_", " "),
		liftTypeKey,
		attemptNumber))

	logging.InfoLogger.Printf("Started recording video: %s", fileName)
	return nil
}

// StopRecording stops the current recording and trims the video
func StopRecording(decisionTime int64) error {
	if currentRecording == nil && !NoVideo {
		return fmt.Errorf("no ongoing recording to stop")
	}

	if NoVideo {
		logging.InfoLogger.Printf("Simulating stop recording video: %s", currentFileName)
	} else {
		// Stop the recording gracefully first
		logging.InfoLogger.Println("Attempting to stop ffmpeg gracefully...")
		if _, err := currentStdin.Write([]byte("q\n")); err != nil { // Added \n after q
			logging.InfoLogger.Printf("Could not write 'q' to ffmpeg (this is normal if process exited): %v", err)
		}

		// Wait a moment to ensure the quit command is processed
		time.Sleep(100 * time.Millisecond)

		// Close stdin pipe
		if err := currentStdin.Close(); err != nil {
			logging.InfoLogger.Printf("Could not close stdin (this is normal if process exited): %v", err)
		}

		// Give ffmpeg a moment to exit gracefully
		done := make(chan error, 1)
		go func() {
			done <- currentRecording.Wait()
		}()

		// Wait up to 2 seconds for graceful exit
		select {
		case err := <-done:
			if err != nil {
				logging.InfoLogger.Printf("ffmpeg exited with error (this is normal): %v", err)
			} else {
				logging.InfoLogger.Println("ffmpeg stopped gracefully")
			}
		case <-time.After(2 * time.Second):
			logging.InfoLogger.Println("ffmpeg did not stop gracefully, killing process...")
			if err := currentRecording.Process.Kill(); err != nil {
				if !strings.Contains(err.Error(), "process already finished") {
					return fmt.Errorf("failed to kill ffmpeg process: %w", err)
				}
			}
		}
	}

	// Compute the difference between start time and stop time, subtract 5 seconds
	startTime := state.LastStartTime
	trimDuration := state.LastTimerStopTime - startTime - 5000 // keep 5 seconds before last clock start
	logging.InfoLogger.Printf("Duration to be trimmed: %d milliseconds", trimDuration)

	// Save the video with an ISO 8601 timestamp without time zone
	timestamp := time.Now().Format("2006-01-02_15h04m05s")
	baseFileName := strings.TrimSuffix(filepath.Base(currentFileName), filepath.Ext(currentFileName))
	baseFileName = baseFileName[:len(baseFileName)-len(fmt.Sprintf("_%d", state.LastStartTime))] // Remove the millis timestamp
	finalFileName := filepath.Join(VideoDir, fmt.Sprintf("%s_%s.mp4", timestamp, baseFileName))

	// Use state information instead of parsing filename
	attemptInfo := fmt.Sprintf("%s - %s attempt %d",
		strings.ReplaceAll(state.CurrentAthlete, "_", " "),
		state.CurrentLiftType,
		state.CurrentAttempt)

	SendStatus(status.Trimming, fmt.Sprintf("Trimming video: %s", attemptInfo))

	var err error
	if startTime == 0 {
		logging.InfoLogger.Println("Start time is 0, not trimming the video")
		if NoVideo {
			logging.InfoLogger.Printf("Simulating rename video: %s -> %s", currentFileName, finalFileName)
		} else if err = os.Rename(currentFileName, finalFileName); err != nil {
			return fmt.Errorf("failed to rename video file to %s: %w", finalFileName, err)
		}
	} else {
		// 5 attempts -- ffmpeg will fail if the input file has not been closed by the previous command
		for i := 0; i < 5; i++ {
			var cmd *exec.Cmd
			recode := false
			args := []string{"-y"}
			if recode {
				args = append(args, "-i", currentFileName,
					"-c:v", "libx264",
					"-crf", "18",
					"-preset", "medium",
					"-profile:v", "main",
					"-pix_fmt", "yuv420p",
					finalFileName)
			} else {
				args = append(args, "-i", currentFileName)
				if trimDuration > 0 {
					args = append(args, "-ss", fmt.Sprintf("%d", trimDuration/1000))
				}
				args = append(args, "-c", "copy", finalFileName)
			}

			cmd = exec.Command(FfmpegPath, args...)
			if i == 0 {
				logging.InfoLogger.Printf("Executing trim command: %s %s", FfmpegPath, strings.Join(args, " "))
			}

			if err = cmd.Run(); err != nil {
				logging.ErrorLogger.Printf("Waiting for input video (attempt %d/5): %v", i+1, err)
				time.Sleep(1 * time.Second)
			} else {
				break
			}
			if i == 4 {
				return fmt.Errorf("failed to open input video after 5 attempts: %w", err)
			}
		}
		if err = os.Remove(currentFileName); err != nil {
			return fmt.Errorf("failed to remove untrimmed video file: %w", err)
		}
	}

	SendStatus(status.Ready, fmt.Sprintf("Video ready: %s", attemptInfo))

	logging.InfoLogger.Printf("Stopped recording and saved video: %s", finalFileName)
	currentRecording = nil
	currentStdin = nil
	currentFileName = ""

	return nil
}

// GetStartTimeMillis returns the start time in milliseconds
func GetStartTimeMillis() string {
	return strconv.FormatInt(state.LastStartTime, 10)
}
