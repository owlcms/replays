package recording

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/state"
	"github.com/owlcms/replays/internal/status"
)

var (
	currentRecording *exec.Cmd
	currentStdin     *os.File
	currentFileName  string
)

// New global variable to hold multiple camera configs.
var CameraConfigs []config.CameraConfiguration

// SetCameraConfigs sets the available camera configurations.
// It also calls SetFfmpegConfig to set the first camera as the default.
func SetCameraConfigs(configs []config.CameraConfiguration) {
	CameraConfigs = configs
	if len(configs) > 0 {
		SetFfmpegConfig(configs[0].FfmpegPath, configs[0].FfmpegCamera, configs[0].Format, configs[0].Params)
	}
}

// buildRecordingArgs builds the ffmpeg arguments for recording
func buildRecordingArgs(fileName string) []string {
	args := []string{
		"-y",               // Overwrite output files
		"-f", FfmpegFormat, // Format
		"-i", FfmpegCamera,
	}

	// Add camera-specific resolution and FPS parameters
	if len(CameraConfigs) > 0 {
		camera := CameraConfigs[0] // Use the first camera's settings
		if camera.Size != "" {
			args = append(args, "-s", camera.Size) // Use -s for resolution
		}
		if camera.Fps > 0 {
			args = append(args, "-r", fmt.Sprintf("%d", camera.Fps)) // Use -r for framerate
		}
	}

	// Add extra parameters if specified
	if FfmpegParams != "" {
		args = append(args, strings.Fields(FfmpegParams)...)
	}

	args = append(args, fileName)
	return args
}

// buildTrimmingArgs builds the ffmpeg arguments for trimming
func buildTrimmingArgs(trimDuration int64, currentFileName, finalFileName string) []string {
	args := []string{"-y"}
	if config.Recode {
		if trimDuration > 0 {
			args = append(args, "-ss", fmt.Sprintf("%d", trimDuration/1000))
		}
		args = append(args,
			"-i", currentFileName,
			"-c:v", "libx264",
			"-crf", "18",
			"-preset", "medium",
			"-profile:v", "main",
			"-pix_fmt", "yuv420p",
		)
	} else {
		if trimDuration > 0 {
			args = append(args, "-ss", fmt.Sprintf("%d", trimDuration/1000))
		}
		args = append(args,
			"-i", currentFileName,
			"-c", "copy",
		)
	}

	if FfmpegParams != "" {
		args = append(args, strings.Fields(FfmpegParams)...)
	}

	args = append(args, finalFileName)
	return args
}

// StartRecording starts recording a video using ffmpeg
func StartRecording(fullName, liftTypeKey string, attemptNumber int) error {
	if err := os.MkdirAll(config.GetVideoDir(), os.ModePerm); err != nil {
		return fmt.Errorf("failed to create video directory: %w", err)
	}

	fullName = strings.ReplaceAll(fullName, " ", "_")

	fileName := filepath.Join(config.GetVideoDir(), fmt.Sprintf("%s_%s_attempt%d_%d.mp4", fullName, liftTypeKey, attemptNumber, state.LastStartTime))

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
	args := buildRecordingArgs(fileName)

	if config.NoVideo {
		cmd = createFfmpegCmd(args)
		logging.InfoLogger.Printf("Simulating start recording video: %s", fileName)
	} else {
		cmd = createFfmpegCmd(args)
		logging.InfoLogger.Printf("Executing command: %s %s", FfmpegPath, strings.Join(args, " "))
	}

	if config.NoVideo {
		logging.InfoLogger.Printf("ffmpeg command: %s", cmd.String())
		currentFileName = fileName
		state.LastTimerStopTime = 0
		return nil
	}

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

	SendStatus(status.Recording, fmt.Sprintf("Recording: %s - %s attempt %d",
		strings.ReplaceAll(fullName, "_", " "),
		liftTypeKey,
		attemptNumber))

	logging.InfoLogger.Printf("Started recording video: %s", fileName)
	return nil
}

// StopRecording stops the current recording and trims the video
func StopRecording(decisionTime int64) error {
	if currentRecording == nil && !config.NoVideo {
		return fmt.Errorf("no ongoing recording to stop")
	}

	if config.NoVideo {
		logging.InfoLogger.Printf("Simulating stop recording video: %s", currentFileName)
	} else {
		logging.InfoLogger.Println("Attempting to stop ffmpeg gracefully...")
		if _, err := currentStdin.Write([]byte("q\n")); err != nil {
			logging.InfoLogger.Printf("Could not write 'q' to ffmpeg (this is normal if process exited): %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		if err := currentStdin.Close(); err != nil {
			logging.InfoLogger.Printf("Could not close stdin (this is normal if process exited): %v", err)
		}

		done := make(chan error, 1)
		go func() {
			done <- currentRecording.Wait()
		}()

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

	startTime := state.LastStartTime
	trimDuration := state.LastTimerStopTime - startTime - 5000
	logging.InfoLogger.Printf("Duration to be trimmed: %d milliseconds", trimDuration)

	timestamp := time.Now().Format("2006-01-02_15h04m05s")
	baseFileName := strings.TrimSuffix(filepath.Base(currentFileName), filepath.Ext(currentFileName))
	baseFileName = baseFileName[:len(baseFileName)-len(fmt.Sprintf("_%d", state.LastStartTime))]
	finalFileName := filepath.Join(config.GetVideoDir(), fmt.Sprintf("%s_%s.mp4", timestamp, baseFileName))

	attemptInfo := fmt.Sprintf("%s - %s attempt %d",
		strings.ReplaceAll(state.CurrentAthlete, "_", " "),
		state.CurrentLiftType,
		state.CurrentAttempt)

	SendStatus(status.Trimming, fmt.Sprintf("Trimming video: %s", attemptInfo))

	var err error
	if startTime == 0 {
		logging.InfoLogger.Println("Start time is 0, not trimming the video")
		if config.NoVideo {
			logging.InfoLogger.Printf("Simulating rename video: %s -> %s", currentFileName, finalFileName)
		} else if err = os.Rename(currentFileName, finalFileName); err != nil {
			return fmt.Errorf("failed to rename video file to %s: %w", finalFileName, err)
		}
	} else {
		for i := 0; i < 5; i++ {
			args := buildTrimmingArgs(trimDuration, currentFileName, finalFileName)
			cmd := createFfmpegCmd(args)

			if i == 0 {
				logging.InfoLogger.Printf("Executing trim command: %s", strings.Join(args, " "))
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
