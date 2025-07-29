package recording

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/httpServer"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/state"
)

var (
	currentRecordings []*exec.Cmd
	currentStdin      []*os.File
	currentFileNames  []string
)

// cleanParams splits a parameter string and removes outer quotes from each parameter
func cleanParams(params string) []string {
	fields := strings.Fields(params)
	cleaned := make([]string, 0, len(fields))

	for _, field := range fields {
		// Remove outer quotes if present
		if (strings.HasPrefix(field, "\"") && strings.HasSuffix(field, "\"")) ||
			(strings.HasPrefix(field, "'") && strings.HasSuffix(field, "'")) {
			field = field[1 : len(field)-1]
		}
		cleaned = append(cleaned, field)
	}
	return cleaned
}

// buildRecordingArgs builds the ffmpeg arguments for recording
func buildRecordingArgs(fileName string, camera config.CameraConfiguration) []string {
	args := []string{
		"-y",                // Overwrite output files
		"-f", camera.Format, // Format
	}

	// Add camera-specific resolution and FPS parameters before input
	if camera.Size != "" {
		args = append(args, "-s", camera.Size) // Use -s for resolution
	}
	if camera.Fps > 0 {
		args = append(args, "-r", fmt.Sprintf("%d", camera.Fps)) // Use -r for framerate
	}

	// Add input source
	args = append(args, "-i", camera.FfmpegCamera)

	// Add extra parameters if specified
	if camera.Params != "" {
		params := cleanParams(camera.Params)
		args = append(args, params...)
	}

	args = append(args, fileName)
	return args
}

// buildTrimmingArgs builds the ffmpeg arguments for trimming
func buildTrimmingArgs(trimDuration int64, currentFileName, finalFileName string, camera config.CameraConfiguration) []string {
	args := []string{"-y"}
	if camera.Recode {
		logging.InfoLogger.Printf("Recode is enabled for camera: %s", camera.FfmpegCamera)
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

	args = append(args, finalFileName)
	return args
}

// StartRecording starts recording videos using ffmpeg for all configured cameras
func StartRecording(fullName, liftTypeKey string, attemptNumber int) error {
	Recording = true
	cameras := config.GetCameraConfigs()
	if len(cameras) == 0 {
		return fmt.Errorf("no camera configurations available")
	}

	if err := os.MkdirAll(config.GetVideoDir(), os.ModePerm); err != nil {
		return fmt.Errorf("failed to create video directory: %w", err)
	}

	fullName = strings.ReplaceAll(fullName, " ", "_")

	var cmds []*exec.Cmd
	var stdins []*os.File
	var fileNames []string

	for i, camera := range cameras {
		fileName := filepath.Join(config.GetVideoDir(), fmt.Sprintf("%s_%s_attempt%d_Camera%d_%d.mkv", fullName, liftTypeKey, attemptNumber, i+1, state.LastStartTime))
		args := buildRecordingArgs(fileName, camera)

		if config.NoVideo {
			cmd := CreateFfmpegCmd(args, "recording")
			logging.InfoLogger.Printf("Simulating start recording video for Camera %d: %s", i+1, cmd.String())
			logging.InfoLogger.Printf("ffmpeg command for Camera %d: %s", i+1, cmd.String())
			fileNames = append(fileNames, fileName)
			state.LastTimerStopTime = 0
			continue
		}

		cmd := CreateFfmpegCmd(args, "recording")
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("failed to create stdin pipe for Camera %d: %w", i+1, err)
		}

		logging.InfoLogger.Printf("Executing command for Camera %d: %s", i+1, cmd.String())
		if err := cmd.Start(); err != nil {
			stdin.Close()
			return fmt.Errorf("failed to start ffmpeg for Camera %d: %w", i+1, err)
		}

		cmds = append(cmds, cmd)
		stdins = append(stdins, stdin.(*os.File))
		fileNames = append(fileNames, fileName)
	}

	currentRecordings = cmds
	currentStdin = stdins
	currentFileNames = fileNames
	state.LastTimerStopTime = 0

	httpServer.SendStatus(httpServer.Recording, fmt.Sprintf("Recording: %s - %s attempt %d",
		strings.ReplaceAll(fullName, "_", " "),
		liftTypeKey,
		attemptNumber))

	logging.InfoLogger.Printf("Started recording videos: %v", fileNames)
	return nil
}

// trimVideo handles the trimming of a single video file
func trimVideo(wg *sync.WaitGroup, i int, currentFileName string, trimDuration int64, startTime int64, fullSessionDir string, timestamp string, finalFileNames *[]string) {
	defer wg.Done()

	baseFileName := strings.TrimSuffix(filepath.Base(currentFileName), filepath.Ext(currentFileName))
	baseFileName = baseFileName[:len(baseFileName)-len(fmt.Sprintf("_%d", state.LastStartTime))]
	finalFileName := filepath.Join(fullSessionDir, fmt.Sprintf("%s_%s.mp4", timestamp, baseFileName))
	*finalFileNames = append(*finalFileNames, finalFileName)

	attemptInfo := fmt.Sprintf("%s - %s attempt %d",
		strings.ReplaceAll(state.CurrentAthlete, "_", " "),
		state.CurrentLiftType,
		state.CurrentAttempt)

	logging.InfoLogger.Printf("Trimming video for Camera %d: %s", i+1, attemptInfo)

	var err error
	if startTime == 0 {
		logging.InfoLogger.Printf("Start time is 0, not trimming the video for Camera %d", i+1)
		if config.NoVideo {
			logging.InfoLogger.Printf("Simulating rename video for Camera %d: %s -> %s", i+1, currentFileName, finalFileName)
		} else if err = os.Rename(currentFileName, finalFileName); err != nil {
			logging.ErrorLogger.Printf("Failed to rename video file for Camera %d to %s: %v", i+1, finalFileName, err)
			return
		}
	} else {
		for j := 0; j < 5; j++ {
			args := buildTrimmingArgs(trimDuration, currentFileName, finalFileName, config.GetCameraConfigs()[i])
			cmd := CreateFfmpegCmd(args, "trimming")

			if j == 0 {
				logging.InfoLogger.Printf("Executing trim command for Camera %d: %s", i+1, cmd.String())
			}

			if err = cmd.Run(); err != nil {
				logging.ErrorLogger.Printf("Waiting for input video for Camera %d (attempt %d/5): %v", i+1, j+1, err)
				time.Sleep(1 * time.Second)
			} else {
				break
			}
			if j == 4 {
				logging.ErrorLogger.Printf("Failed to open input video for Camera %d after 5 attempts: %v", i+1, err)
				httpServer.SendStatus(httpServer.Ready, fmt.Sprintf("Error: Failed to trim video for Camera %d after 5 attempts", i+1))
				return
			}
		}
		if err = os.Remove(currentFileName); err != nil {
			logging.ErrorLogger.Printf("Failed to remove untrimmed video file for Camera %d: %v", i+1, err)
			return
		}
	}
}

// StopRecordingAndTrim stops the current recordings and trims the videos
func StopRecordingAndTrim(decisionTime int64) error {
	shouldReturn, err := StopRecording()
	if shouldReturn {
		return err
	}

	startTime := state.LastStartTime
	trimDuration := state.LastTimerStopTime - startTime - 5000
	logging.InfoLogger.Printf("Duration to be trimmed: %d milliseconds", trimDuration)

	timestamp := time.Now().Format("2006-01-02_15h04m05s")
	var finalFileNames []string

	// Create session directory if it doesn't exist
	sessionDir := state.CurrentSession
	if sessionDir == "" {
		sessionDir = "unsorted"
	}
	sessionDir = strings.ReplaceAll(sessionDir, " ", "_")
	fullSessionDir := filepath.Join(config.GetVideoDir(), sessionDir)
	if err := os.MkdirAll(fullSessionDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	// Update status to "Trimming videos for XXX attempt YYY"
	statusMessage := fmt.Sprintf("Trimming videos for %s -- %s attempt %d", state.CurrentAthlete, state.CurrentLiftType, state.CurrentAttempt)
	httpServer.SendStatus(httpServer.Trimming, statusMessage)

	var wg sync.WaitGroup
	for i, currentFileName := range currentFileNames {
		wg.Add(1)
		go trimVideo(&wg, i, currentFileName, trimDuration, startTime, fullSessionDir, timestamp, &finalFileNames)
	}

	wg.Wait()

	// Send single "Videos ready" message after all cameras are done
	httpServer.SendStatus(httpServer.Ready, "Videos ready")

	logging.InfoLogger.Printf("Stopped recording and saved videos: %v", finalFileNames)
	currentRecordings = nil
	currentStdin = nil
	currentFileNames = nil

	return nil
}

func StopRecording() (bool, error) {
	Recording = false
	if len(currentRecordings) == 0 && !config.NoVideo {
		return true, fmt.Errorf("no ongoing recordings to stop")
	}

	if config.NoVideo {
		for i, fileName := range currentFileNames {
			logging.InfoLogger.Printf("Simulating stop recording video for Camera %d: %s", i+1, fileName)
		}
	} else {
		logging.InfoLogger.Println("Attempting to stop ffmpeg gracefully...")
		for i, stdin := range currentStdin {
			if _, err := stdin.Write([]byte("q\n")); err != nil {
				logging.InfoLogger.Printf("Could not write 'q' to ffmpeg for Camera %d (this is normal if process exited): %v", i+1, err)
			}
		}
		time.Sleep(100 * time.Millisecond)
		for i, stdin := range currentStdin {
			if err := stdin.Close(); err != nil {
				logging.InfoLogger.Printf("Could not close stdin for Camera %d (this is normal if process exited): %v", i+1, err)
			}
		}
		var wg sync.WaitGroup
		for i, cmd := range currentRecordings {
			wg.Add(1)
			go func(i int, cmd *exec.Cmd) {
				defer wg.Done()
				if err := cmd.Wait(); err != nil {
					logging.InfoLogger.Printf("ffmpeg exited with error for Camera %d (this is normal): %v", i+1, err)
				} else {
					logging.InfoLogger.Printf("ffmpeg stopped gracefully for Camera %d", i+1)
				}
			}(i, cmd)
		}
		wg.Wait()
	}
	return false, nil
}

func TerminateRecordings() {
	if config.NoVideo {
		for i, fileName := range currentFileNames {
			logging.InfoLogger.Printf("Simulating forced stop recording video for Camera %d: %s", i+1, fileName)
		}
	} else {
		logging.InfoLogger.Println("Forcing stop ffmpeg if required...")
		for i, stdin := range currentStdin {
			logging.InfoLogger.Printf("Attempting to stop ffmpeg %d gracefully...", i+1)
			if _, err := stdin.Write([]byte("q\n")); err != nil {
				logging.InfoLogger.Printf("Could not write 'q' to ffmpeg for Camera %d (this is normal if process exited): %v", i+1, err)
			}
		}

		time.Sleep(100 * time.Millisecond)

		var wg sync.WaitGroup
		for i, cmd := range currentRecordings {
			wg.Add(1)
			go func(i int, cmd *exec.Cmd) {
				defer wg.Done()
				if err := forceKillCmd(cmd); err != nil {
					logging.InfoLogger.Printf("ffmpeg exited for Camera %d: %v", i+1, err)
				} else {
					logging.InfoLogger.Printf("ffmpeg stopped gracefully for Camera %d", i+1)
				}
			}(i, cmd)
		}

		wg.Wait()
	}
}

// GetStartTimeMillis returns the start time in milliseconds
func GetStartTimeMillis() string {
	return strconv.FormatInt(state.LastStartTime, 10)
}

// ListCameras lists available cameras using ffmpeg on Windows or v4l2-ctl on Linux and displays them in a Fyne text area
func ListCameras(window fyne.Window) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		args := []string{"-list_devices", "true", "-f", "dshow", "-i", "dummy", "-hide_banner"}
		cmd = CreateFfmpegCmd(args, "listcameras", "info")
		logging.InfoLogger.Printf("ListCameras: Using ffmpeg path: %s", config.GetFFmpegPath())
		logging.InfoLogger.Printf("ListCameras: Install directory: %s", config.GetInstallDir())
		logging.InfoLogger.Printf("ListCameras: Command: %s", cmd.String())
	case "linux":
		cmd = exec.Command("v4l2-ctl", "--list-devices")
		logging.InfoLogger.Printf("ListCameras: Using v4l2-ctl command: %s", cmd.String())
	default:
		dialog.ShowInformation("Unsupported Platform", "Camera listing is not supported on this platform.", window)
		return
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	logging.InfoLogger.Printf("ListCameras: About to execute command...")
	if err := cmd.Run(); err != nil {
		logging.ErrorLogger.Printf("Failed to list cameras: %v", err)
		logging.ErrorLogger.Printf("Command output: %s", out.String())
		dialog.ShowError(fmt.Errorf("failed to list cameras: %v\nOutput: %s", err, out.String()), window)
		return
	}
	logging.InfoLogger.Printf("ListCameras: Command executed successfully")
	logging.InfoLogger.Printf("ListCameras: Raw output length: %d bytes", out.Len())
	logging.InfoLogger.Printf("ListCameras: Raw output: %s", out.String())

	var cameraNames []string
	scanner := bufio.NewScanner(&out)
	switch runtime.GOOS {
	case "windows":
		logging.InfoLogger.Printf("ListCameras: Parsing Windows ffmpeg output...")
		lineCount := 0
		for scanner.Scan() {
			line := scanner.Text()
			lineCount++
			logging.InfoLogger.Printf("ListCameras: Line %d: %s", lineCount, line)
			if strings.Contains(line, "(video)") {
				logging.InfoLogger.Printf("ListCameras: Found video device line: %s", line)
				start := strings.Index(line, "\"")
				end := strings.LastIndex(line, "\"")
				if start != -1 && end != -1 && start != end {
					cameraName := line[start+1 : end]
					logging.InfoLogger.Printf("ListCameras: Extracted camera name: %s", cameraName)
					cameraNames = append(cameraNames, cameraName)
				} else {
					logging.InfoLogger.Printf("ListCameras: Could not extract camera name from line (no quotes found)")
				}
			}
		}
		logging.InfoLogger.Printf("ListCameras: Processed %d lines total", lineCount)
	case "linux":
		logging.InfoLogger.Printf("ListCameras: Parsing Linux v4l2-ctl output...")
		var currentCamera string
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			logging.InfoLogger.Printf("ListCameras: Processing line: %s", line)
			if strings.Contains(line, "(usb-") {
				currentCamera = strings.Split(line, " (usb-")[0]
				logging.InfoLogger.Printf("ListCameras: Found camera: %s", currentCamera)
			} else if strings.HasPrefix(line, "/dev/video") && currentCamera != "" {
				cameraEntry := fmt.Sprintf("%s: %s", currentCamera, line)
				logging.InfoLogger.Printf("ListCameras: Adding camera entry: %s", cameraEntry)
				cameraNames = append(cameraNames, cameraEntry)
				currentCamera = ""
			}
		}
	}

	logging.InfoLogger.Printf("ListCameras: Found %d cameras total", len(cameraNames))
	for i, name := range cameraNames {
		logging.InfoLogger.Printf("ListCameras: Camera %d: %s", i+1, name)
	}

	if len(cameraNames) == 0 {
		dialog.ShowInformation("No Cameras Found", "No cameras were found on this system.", window)
		return
	}

	cameraList := strings.Join(cameraNames, "\n")
	textArea := widget.NewMultiLineEntry()
	textArea.SetMinRowsVisible(4)
	textArea.SetText(cameraList)
	textArea.Wrapping = fyne.TextWrapWord

	dialog := dialog.NewCustom("Available Cameras", "Close", container.NewVBox(
		widget.NewLabel("The following cameras were found on this system:"),
		textArea,
	), window)
	dialog.Resize(fyne.NewSize(400, 300)) // Increased height by 25%
	dialog.Show()
}
