package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/jobutil"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/recording"
)

var (
	includeAll bool
	startPort  int

	previewMu   sync.Mutex
	previewCmds []*exec.Cmd

	// camerasConfig is loaded at startup from cameras.toml
	camerasConfig *config.CamerasConfig
)

type cameraStream struct {
	camera  recording.DetectedCamera
	port    int
	udpDest string
	cmd     *exec.Cmd
	encoder *recording.HwEncoder

	mu         sync.RWMutex
	running    bool
	status     string
	fps        string
	frame      string
	bitrate    string
	speed      string
	lastStderr string
	lastUpdate time.Time
}

var (
	fpsRegex     = regexp.MustCompile(`fps=\s*([0-9.]+)`)
	frameRegex   = regexp.MustCompile(`frame=\s*([0-9]+)`)
	bitrateRegex = regexp.MustCompile(`bitrate=\s*([^\s]+)`)
	speedRegex   = regexp.MustCompile(`speed=\s*([^\s]+)`)
)

func main() {
	// Parse command-line flags
	flag.BoolVar(&includeAll, "all", false, "Include all cameras, including raw formats (typically integrated cameras)")
	flag.IntVar(&startPort, "startport", 0, "Starting port for multicast allocation (overrides cameras.toml)")
	flag.Parse()

	// Create a job object so child processes die with us
	if err := jobutil.Init(); err != nil {
		fmt.Printf("Warning: Failed to create job object: %v\n", err)
	}

	// Initialize logging to the shared install-dir logs folder (same as replays)
	logDir := filepath.Join(config.GetInstallDir(), "logs")
	if err := logging.InitWithFile(logDir, "cameras.log"); err != nil {
		fmt.Printf("Warning: Failed to initialize logging: %v\n", err)
	} else {
		fmt.Printf("Writing logs to: %s\n", filepath.Join(logDir, "cameras.log"))
	}

	// Load cameras.toml configuration
	cfg, err := config.LoadCamerasConfig()
	if err != nil {
		fmt.Printf("Error loading cameras.toml: %v\n", err)
		fmt.Println("Using built-in defaults.")
		cfg = &config.CamerasConfig{}
	}
	camerasConfig = cfg

	// Apply CLI overrides
	if includeAll {
		camerasConfig.Cameras.IncludeAll = true
	}
	if startPort > 0 {
		camerasConfig.Multicast.StartPort = startPort
	}

	// Initialize ffmpeg path
	if err := recording.InitializeFFmpeg(); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}

	fmt.Println("Detecting cameras...")

	// Detect cameras
	cameras := recording.DetectCameras()
	if len(cameras) == 0 {
		fmt.Println("No cameras detected.")
		return
	}

	// Detect encoders using config-defined encoder list
	encoders := recording.DetectEncodersWithConfig(camerasConfig)
	bestEncoder := recording.PickBestEncoder(encoders)

	if bestEncoder != nil {
		logging.InfoLogger.Printf("Best encoder: %s (%s)", bestEncoder.Name, bestEncoder.Description)
	} else {
		logging.InfoLogger.Printf("No hardware encoder available, will use software: %s", camerasConfig.Software.OutputParameters)
	}

	// Filter out integrated cameras
	var filtered []recording.DetectedCamera
	for _, cam := range cameras {
		if !camerasConfig.Cameras.IncludeAll && isIntegratedCamera(cam) {
			fmt.Printf("Skipping integrated camera: %s (%s)\n", cam.Name, cam.PixFmt)
			continue
		}
		filtered = append(filtered, cam)
	}

	if len(filtered) == 0 {
		fmt.Println("No suitable cameras found (all are integrated cameras).")
		return
	}

	// Sort cameras by format priority from config
	sort.Slice(filtered, func(i, j int) bool {
		return camerasConfig.FormatPriorityValue(filtered[i].PixFmt) > camerasConfig.FormatPriorityValue(filtered[j].PixFmt)
	})

	// Start ffmpeg for each camera
	streams := startAllStreams(filtered, bestEncoder)

	if len(streams) == 0 {
		fmt.Println("\nNo streams started successfully.")
		return
	}

	fmt.Printf("\n%d camera(s) streaming. Launching status window...\n", len(streams))
	runUI(streams, filtered, bestEncoder)
}

// startAllStreams starts multicast streams for all cameras.
// Returns only the streams that started successfully.
func startAllStreams(cameras []recording.DetectedCamera, encoder *recording.HwEncoder) []*cameraStream {
	port := camerasConfig.Multicast.StartPort
	var streams []*cameraStream

	fmt.Println("\nStarting camera streams:")
	fmt.Println("========================")

	for _, cam := range cameras {
		udpDest := fmt.Sprintf("udp://%s:%d", camerasConfig.Multicast.IP, port)
		stream := &cameraStream{
			camera:  cam,
			port:    port,
			encoder: encoder,
			udpDest: udpDest,
			status:  "starting",
			running: false,
			fps:     "-",
			frame:   "-",
			bitrate: "-",
			speed:   "-",
		}

		fmt.Printf("\n[%s] %s (%s, %s @ %d fps)\n", cam.PixFmt, cam.Name, cam.Size, cam.PixFmt, cam.Fps)
		fmt.Printf("  -> %s\n", udpDest)

		cmd, err := startStream(stream)
		if err != nil {
			fmt.Printf("  ERROR: Failed to start stream: %v\n", err)
			stream.setStopped(fmt.Sprintf("failed: %v", err))
		} else {
			stream.cmd = cmd
			stream.setRunning()
			streams = append(streams, stream)
		}

		port++
	}

	return streams
}

// isIntegratedCamera checks if a camera is likely an integrated webcam.
// Primary indicator: raw pixel formats (yuyv422, nv12, rgb24) are typically from integrated cameras.
// External/professional cameras usually offer mjpeg or h264.
func isIntegratedCamera(cam recording.DetectedCamera) bool {
	// Raw pixel formats are the primary indicator of integrated cameras
	switch cam.PixFmt {
	case "yuyv422", "nv12", "rgb24", "bgr24", "uyvy422":
		// Raw format - likely integrated camera
		return true
	case "h264", "mjpeg":
		// Compressed format - external camera, check name just in case
		lower := strings.ToLower(cam.Name)
		keywords := []string{
			"integrated",
			"internal",
			"built-in",
			"builtin",
			"ir camera",     // IR cameras often on laptops
			"windows hello", // Windows Hello cameras
			"front camera",  // Tablet/laptop front cameras
			"face",          // Face recognition cameras
		}
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				return true
			}
		}
		return false
	default:
		// Unknown format - assume integrated if name matches
		lower := strings.ToLower(cam.Name)
		keywords := []string{
			"integrated",
			"internal",
			"built-in",
			"builtin",
			"ir camera",
			"windows hello",
			"front camera",
			"face",
		}
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				return true
			}
		}
		return true // Unknown raw format - assume integrated
	}
}

// startStream starts ffmpeg to stream a camera to multicast UDP
func startStream(stream *cameraStream) (*exec.Cmd, error) {
	cam := stream.camera
	encoder := stream.encoder
	port := stream.port
	cfg := camerasConfig

	ffmpegPath := config.GetFFmpegPath()
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}

	udpDest := fmt.Sprintf("udp://%s:%d?pkt_size=%d", cfg.Multicast.IP, port, cfg.Multicast.PktSize)

	var args []string

	// Build input arguments based on platform and format
	switch cam.Format {
	case "dshow":
		args = append(args, "-f", "dshow")
		// For dshow on Windows: compressed camera modes use -vcodec, raw modes use -pixel_format
		switch cam.PixFmt {
		case "mjpeg":
			args = append(args, "-vcodec", "mjpeg")
		case "h264":
			// Do not force h264 input codec for dshow; device negotiation is more reliable without it.
		default:
			args = append(args, "-pixel_format", cam.PixFmt)
		}
		args = append(args, "-video_size", cam.Size)
		args = append(args, "-framerate", fmt.Sprintf("%d", cam.Fps))
		args = append(args, "-rtbufsize", "512M")
		args = append(args, "-i", fmt.Sprintf("video=%s", cam.Device))

	case "v4l2":
		args = append(args, "-f", "v4l2")
		switch cam.PixFmt {
		case "mjpeg":
			args = append(args, "-input_format", "mjpeg")
		case "h264":
			args = append(args, "-input_format", "h264")
		default:
			args = append(args, "-input_format", cam.PixFmt)
		}
		args = append(args, "-video_size", cam.Size)
		args = append(args, "-framerate", fmt.Sprintf("%d", cam.Fps))
		args = append(args, "-i", cam.Device)
	}

	// Build output arguments based on input format
	gopSize := cam.Fps * cfg.Output.GopMultiplier

	switch cam.PixFmt {
	case "h264":
		// Camera already outputs H.264 — just remux, no re-encode needed.
		args = append(args, "-c:v", "copy")

	case "mjpeg":
		// Need to decode MJPEG and encode to H.264
		if encoder != nil {
			args = append(args, strings.Fields(encoder.OutputParameters)...)
		} else {
			args = append(args, strings.Fields(cfg.Software.OutputParameters)...)
		}
		args = append(args, "-g", fmt.Sprintf("%d", gopSize))
		args = append(args, "-keyint_min", fmt.Sprintf("%d", gopSize))

	default:
		// Raw format - need to encode
		if encoder != nil {
			args = append(args, strings.Fields(encoder.OutputParameters)...)
		} else {
			args = append(args, strings.Fields(cfg.Software.OutputParameters)...)
		}
		args = append(args, "-g", fmt.Sprintf("%d", gopSize))
		args = append(args, "-keyint_min", fmt.Sprintf("%d", gopSize))
	}

	// Output flags from config (e.g. "-an -f mpegts")
	args = append(args, strings.Fields(cfg.Output.ExtraFlags)...)
	args = append(args, udpDest)

	// Create the command with hidden console on Windows
	cmd := recording.CreateHiddenCmd(ffmpegPath, args...)
	cmd.Stdout = io.Discard

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	logging.InfoLogger.Printf("Starting ffmpeg: %s %v", ffmpegPath, args)

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	if err := jobutil.Assign(cmd); err != nil {
		logging.ErrorLogger.Printf("Failed to assign ffmpeg to job object: %v", err)
	}

	go monitorFFmpegStats(stream, stderr)
	go func() {
		err := cmd.Wait()
		if err != nil {
			lastErr := stream.getLastStderr()
			if lastErr != "" {
				logging.ErrorLogger.Printf("ffmpeg exited for %s (%s): %v | last stderr: %s", stream.camera.Name, stream.udpDest, err, lastErr)
			} else {
				logging.ErrorLogger.Printf("ffmpeg exited for %s (%s): %v", stream.camera.Name, stream.udpDest, err)
			}
			stream.setStopped(fmt.Sprintf("stopped: %v", err))
			return
		}
		stream.setStopped("stopped")
	}()

	return cmd, nil
}

// stopProcess gracefully stops an ffmpeg process
func stopProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	// Try graceful shutdown first
	if runtime.GOOS == "windows" {
		// On Windows, use taskkill
		kill := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
		kill.Run()
	} else {
		// On Unix, send SIGTERM then SIGKILL
		cmd.Process.Signal(syscall.SIGTERM)
		// Give it a moment then force kill
		cmd.Process.Kill()
	}
}

func splitCRLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func monitorFFmpegStats(stream *cameraStream, stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	scanner.Split(splitCRLF)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		stream.setLastStderr(line)
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "unable") || strings.Contains(lower, "invalid") || strings.Contains(lower, "permission denied") || strings.Contains(lower, "device or resource busy") {
			logging.ErrorLogger.Printf("ffmpeg stderr [%s]: %s", stream.camera.Name, line)
		}

		if strings.Contains(line, "frame=") || strings.Contains(line, "fps=") {
			stream.updateStats(line)
		}
	}
}

func parseRegexValue(re *regexp.Regexp, line string) (string, bool) {
	matches := re.FindStringSubmatch(line)
	if len(matches) < 2 {
		return "", false
	}
	return matches[1], true
}

func (s *cameraStream) setRunning() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = true
	s.status = "running"
	s.lastUpdate = time.Now()
}

func (s *cameraStream) setStopped(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.status = status
	s.lastUpdate = time.Now()
}

func (s *cameraStream) setLastStderr(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastStderr = line
}

func (s *cameraStream) getLastStderr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastStderr
}

func (s *cameraStream) updateStats(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if value, ok := parseRegexValue(frameRegex, line); ok {
		s.frame = value
	}
	if value, ok := parseRegexValue(fpsRegex, line); ok {
		s.fps = value
	}
	if value, ok := parseRegexValue(bitrateRegex, line); ok {
		s.bitrate = value
	}
	if value, ok := parseRegexValue(speedRegex, line); ok {
		s.speed = value
	}

	s.running = true
	s.status = "running"
	s.lastUpdate = time.Now()
}

func formatFPSValue(raw string) string {
	if raw == "" || raw == "-" {
		return raw
	}
	if !strings.Contains(raw, ".") {
		return raw
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return raw
	}
	return fmt.Sprintf("%.2f", value)
}

func (s *cameraStream) snapshotRow() [9]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	age := "-"
	if !s.lastUpdate.IsZero() {
		age = strconv.Itoa(int(time.Since(s.lastUpdate).Seconds())) + "s"
	}

	return [9]string{
		s.camera.Name,
		s.camera.PixFmt,
		s.camera.Size,
		s.udpDest,
		formatFPSValue(s.fps),
		"Preview",
		"Record 10s",
		s.status,
		age,
	}
}

func parseResolution(size string) (int, int, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	height, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func resolveFFplayPath() string {
	ffmpegPath := config.GetFFmpegPath()
	if ffmpegPath == "" {
		if runtime.GOOS == "windows" {
			return "ffplay.exe"
		}
		return "ffplay"
	}

	ffplayName := "ffplay"
	if runtime.GOOS == "windows" {
		ffplayName = "ffplay.exe"
	}

	candidate := filepath.Join(filepath.Dir(ffmpegPath), ffplayName)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}

	return ffplayName
}

func registerPreviewCmd(cmd *exec.Cmd) {
	previewMu.Lock()
	previewCmds = append(previewCmds, cmd)
	previewMu.Unlock()
}

func unregisterPreviewCmd(cmd *exec.Cmd) {
	previewMu.Lock()
	defer previewMu.Unlock()

	for i, existing := range previewCmds {
		if existing == cmd {
			previewCmds = append(previewCmds[:i], previewCmds[i+1:]...)
			return
		}
	}
}

func stopAllPreviews() {
	previewMu.Lock()
	cmds := append([]*exec.Cmd(nil), previewCmds...)
	previewMu.Unlock()

	for _, cmd := range cmds {
		stopProcess(cmd)
	}
}

func launchPreview(stream *cameraStream) error {
	args := []string{"-fflags", "nobuffer", "-flags", "low_delay"}
	if width, height, ok := parseResolution(stream.camera.Size); ok {
		args = append(args, "-x", strconv.Itoa(width), "-y", strconv.Itoa(height))
	}
	args = append(args, stream.udpDest)

	ffplayPath := resolveFFplayPath()
	cmd := recording.CreateHiddenCmd(ffplayPath, args...)
	if err := cmd.Start(); err != nil {
		return err
	}

	if err := jobutil.Assign(cmd); err != nil {
		logging.ErrorLogger.Printf("Failed to assign ffplay to job object: %v", err)
	}

	registerPreviewCmd(cmd)
	go func() {
		_ = cmd.Wait()
		unregisterPreviewCmd(cmd)
	}()

	return nil
}

func sanitizeFilePart(value string) string {
	replacer := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_", ";", "_", "|", "_", "?", "_", "*", "_")
	cleaned := replacer.Replace(strings.TrimSpace(value))
	if cleaned == "" {
		return "camera"
	}
	return cleaned
}

func buildClipPath(stream *cameraStream) string {
	timestamp := time.Now().Format("20060102-150405")
	cameraName := sanitizeFilePart(stream.camera.Name)
	return fmt.Sprintf("/tmp/%s_%s.mp4", cameraName, timestamp)
}

func recordClip(stream *cameraStream) (string, error) {
	outputPath := buildClipPath(stream)
	args := []string{
		"-y",
		"-t", "10",
		"-i", stream.udpDest,
		"-c", "copy",
		"-movflags", "+faststart",
		outputPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return outputPath, nil
}

func runUI(streams []*cameraStream, cameras []recording.DetectedCamera, encoder *recording.HwEncoder) {
	myApp := app.New()
	window := myApp.NewWindow("Camera Multicast Streams")
	window.Resize(fyne.NewSize(1320, 500))

	headers := []string{"Camera", "Format", "Size", "Multicast", "FPS", "Preview", "Record", "Status", "Last"}
	actionStatus := widget.NewLabel("Preview/Record: ready")

	// Starting port entry + restart button
	portEntry := widget.NewEntry()
	portEntry.SetText(strconv.Itoa(camerasConfig.Multicast.StartPort))
	portEntry.Validator = func(s string) error {
		if _, err := strconv.Atoi(s); err != nil {
			return fmt.Errorf("must be a number")
		}
		return nil
	}

	// We need a mutable reference so the table and restart can update it
	currentStreams := &streams

	restartBtn := widget.NewButton("Restart Streams", nil)

	table := widget.NewTable(
		func() (int, int) {
			return len(*currentStreams) + 1, len(headers)
		},
		func() fyne.CanvasObject {
			label := widget.NewLabel("template")
			button := widget.NewButton("Preview", nil)
			button.Hide()
			return container.NewMax(label, button)
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			cell := obj.(*fyne.Container)
			label := cell.Objects[0].(*widget.Label)
			button := cell.Objects[1].(*widget.Button)

			if id.Row == 0 {
				button.Hide()
				label.TextStyle = fyne.TextStyle{Bold: true}
				label.Show()
				label.SetText(headers[id.Col])
				return
			}

			ss := *currentStreams
			if id.Row-1 >= len(ss) {
				button.Hide()
				label.Show()
				label.SetText("")
				return
			}
			row := ss[id.Row-1].snapshotRow()
			if id.Col == 5 {
				label.Hide()
				button.Show()
				button.SetText("Preview")
				stream := ss[id.Row-1]
				button.OnTapped = func() {
					if err := launchPreview(stream); err != nil {
						actionStatus.SetText(fmt.Sprintf("Preview failed: %v", err))
						logging.ErrorLogger.Printf("Failed to start ffplay preview for %s (%s): %v", stream.camera.Name, stream.udpDest, err)
						return
					}
					actionStatus.SetText(fmt.Sprintf("Previewing: %s (%s)", stream.camera.Name, stream.camera.Size))
				}
				return
			}

			if id.Col == 6 {
				label.Hide()
				button.Show()
				button.SetText("Record 10s")
				stream := ss[id.Row-1]
				button.OnTapped = func() {
					actionStatus.SetText(fmt.Sprintf("Recording 10s: %s", stream.camera.Name))
					go func(s *cameraStream) {
						outputPath, err := recordClip(s)
						if err != nil {
							actionStatus.SetText(fmt.Sprintf("Record failed: %v", err))
							logging.ErrorLogger.Printf("Failed to record clip for %s (%s): %v", s.camera.Name, s.udpDest, err)
							return
						}
						actionStatus.SetText(fmt.Sprintf("Saved clip: %s", outputPath))
					}(stream)
				}
				return
			}

			button.Hide()
			label.Show()
			label.TextStyle = fyne.TextStyle{}
			label.SetText(row[id.Col])
		},
	)

	table.SetColumnWidth(0, 260)
	table.SetColumnWidth(1, 80)
	table.SetColumnWidth(2, 90)
	table.SetColumnWidth(3, 250)
	table.SetColumnWidth(4, 70)
	table.SetColumnWidth(5, 90)
	table.SetColumnWidth(6, 110)
	table.SetColumnWidth(7, 220)
	table.SetColumnWidth(8, 60)

	stopAll := func() {
		stopAllPreviews()
		for _, stream := range *currentStreams {
			if stream.cmd != nil && stream.cmd.Process != nil {
				stopProcess(stream.cmd)
			}
		}
	}

	// Wire up the restart button
	restartBtn.OnTapped = func() {
		newPort, err := strconv.Atoi(strings.TrimSpace(portEntry.Text))
		if err != nil || newPort < 1 || newPort > 65535 {
			actionStatus.SetText("Invalid starting port")
			return
		}
		actionStatus.SetText("Restarting streams...")

		// Stop existing streams
		stopAllPreviews()
		for _, stream := range *currentStreams {
			if stream.cmd != nil && stream.cmd.Process != nil {
				stopProcess(stream.cmd)
			}
		}
		time.Sleep(500 * time.Millisecond) // let processes exit

		// Update config and restart
		camerasConfig.Multicast.StartPort = newPort
		newStreams := startAllStreams(cameras, encoder)

		*currentStreams = newStreams
		table.Refresh()
		actionStatus.SetText(fmt.Sprintf("%d stream(s) restarted on port %d+", len(newStreams), newPort))
	}

	stopped := false
	stopOnce := sync.Once{}
	closeFn := func() {
		stopOnce.Do(func() {
			stopped = true
			stopAll()
		})
	}

	ticker := time.NewTicker(1 * time.Second)
	go func() {
		for range ticker.C {
			if stopped {
				return
			}
			table.Refresh()
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		closeFn()
		ticker.Stop()
		window.Close()
	}()

	window.SetCloseIntercept(func() {
		closeFn()
		ticker.Stop()
		window.Close()
	})

	portRow := container.NewHBox(
		widget.NewLabel("Starting port:"),
		portEntry,
		restartBtn,
	)

	content := container.NewBorder(
		container.NewVBox(
			widget.NewLabel("Live ffmpeg stream stats (FPS from ffmpeg stderr progress lines)"),
			portRow,
			actionStatus,
		),
		nil,
		nil,
		nil,
		table,
	)

	window.SetContent(content)
	window.ShowAndRun()
}
