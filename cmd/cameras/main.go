package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
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
	"github.com/owlcms/replays/internal/assets"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/jobutil"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/recording"
)

var (
	includeAll    bool
	startPort     int
	extractConfig bool

	previewMu   sync.Mutex
	previewCmds []*exec.Cmd

	// camerasConfig is loaded at startup from shared cameras_config.toml
	// overlaid with instance cameras.toml settings.
	camerasConfig *config.CamerasConfig
)

func setAppIcon(myApp fyne.App) {
	if assets.IconResource != nil && len(assets.IconResource.Content()) > 0 {
		myApp.SetIcon(assets.IconResource)
		return
	}

	iconCandidates := make([]string, 0, 2)

	if exePath, err := os.Executable(); err == nil {
		iconCandidates = append(iconCandidates, filepath.Join(filepath.Dir(exePath), "Icon.png"))
	}
	if wd, err := os.Getwd(); err == nil {
		iconCandidates = append(iconCandidates, filepath.Join(wd, "Icon.png"))
	}

	for _, iconPath := range iconCandidates {
		res, err := fyne.LoadResourceFromPath(iconPath)
		if err == nil {
			myApp.SetIcon(res)
			return
		}
	}
}

type cameraStream struct {
	camera  recording.DetectedCamera
	port    int
	udpDest string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	encoder *recording.HwEncoder

	mu                 sync.RWMutex
	running            bool
	status             string
	fps                string
	metricName         string
	frame              string
	bitrate            string
	speed              string
	progressFrame      int64
	hasProgressFrame   bool
	progressOutTimeUS  int64
	hasProgressOutTime bool
	lastProgress       Progress
	hasLastProgress    bool
	fpsEMA             float64
	hasFPSEMA          bool
	driftEMA           float64
	hasDriftEMA        bool
	lastStderr         string
	lastUpdate         time.Time
	stopping           bool
}

type Progress struct {
	Frame     int64
	OutTimeUS int64 // microseconds (ffmpeg out_time_ms is actually µs)
	WallTime  time.Time
}

type Metrics struct {
	FPS   float64
	Drift float64
}

func ComputeMetrics(prev, curr Progress) Metrics {
	deltaFrame := curr.Frame - prev.Frame
	deltaOutSeconds := float64(curr.OutTimeUS-prev.OutTimeUS) / 1_000_000.0
	deltaWallSeconds := curr.WallTime.Sub(prev.WallTime).Seconds()

	var fps float64
	if deltaOutSeconds > 0 {
		fps = float64(deltaFrame) / deltaOutSeconds
	}

	var drift float64
	if deltaWallSeconds > 0 {
		drift = deltaOutSeconds / deltaWallSeconds
	}

	return Metrics{
		FPS:   fps,
		Drift: drift,
	}
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
	flag.BoolVar(&extractConfig, "extractConfig", false, "extract default editable config files to configDir/install dir and exit")
	flag.StringVar(&config.ConfigDir, "configDir", "", "directory containing editable camera config files")
	flag.Parse()

	if config.ConfigDir != "" {
		if absConfigDir, err := filepath.Abs(config.ConfigDir); err == nil {
			config.ConfigDir = absConfigDir
		}
	}

	if extractConfig {
		if p := config.ExtractDefaultCamerasConfig(); p == "" {
			fmt.Println("Failed to extract camera config files")
			os.Exit(1)
		}
		fmt.Printf("Extracted camera config files in: %s\n", config.GetInstallDir())
		return
	}

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

	// Load merged cameras configuration (shared + instance)
	cfg, err := config.LoadCamerasConfig()
	if err != nil {
		fmt.Printf("Error loading cameras config: %v\n", err)
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

func multicastOutputURL(multicast config.MulticastConfig, port int) string {
	url := fmt.Sprintf("udp://%s:%d?pkt_size=%d", multicast.IP, port, multicast.PktSize)
	if multicast.LocalOnly {
		url += "&ttl=0"
	}
	return url
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

	udpDest := multicastOutputURL(cfg.Multicast, port)

	var args []string

	// Determine if we need hardware encoding (not h264 copy mode)
	needsEncoding := cam.PixFmt != "h264"

	// Add encoder input parameters BEFORE the input specification
	// This is required for hardware encoders like vaapi that need hwaccel init
	if needsEncoding && encoder != nil && encoder.InputParameters != "" {
		args = append(args, strings.Fields(encoder.InputParameters)...)
	}

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
		// Only add rtbufsize if not already in encoder input params
		if encoder == nil || !strings.Contains(encoder.InputParameters, "rtbufsize") {
			args = append(args, "-rtbufsize", "512M")
		}
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
			// Add hwupload filter for hardware encoders that need it (vaapi, qsv)
			if strings.Contains(encoder.Name, "vaapi") {
				args = append(args, "-vf", "format=nv12,hwupload")
			} else if strings.Contains(encoder.Name, "qsv") {
				args = append(args, "-vf", "format=nv12,hwupload=extra_hw_frames=64")
			}
			args = append(args, strings.Fields(encoder.OutputParameters)...)
		} else {
			args = append(args, strings.Fields(cfg.Software.OutputParameters)...)
		}
		args = append(args, "-g", fmt.Sprintf("%d", gopSize))
		args = append(args, "-keyint_min", fmt.Sprintf("%d", gopSize))

	default:
		// Raw format - need to encode
		if encoder != nil {
			// Add hwupload filter for hardware encoders that need it (vaapi, qsv)
			if strings.Contains(encoder.Name, "vaapi") {
				args = append(args, "-vf", "format=nv12,hwupload")
			} else if strings.Contains(encoder.Name, "qsv") {
				args = append(args, "-vf", "format=nv12,hwupload=extra_hw_frames=64")
			}
			args = append(args, strings.Fields(encoder.OutputParameters)...)
		} else {
			args = append(args, strings.Fields(cfg.Software.OutputParameters)...)
		}
		args = append(args, "-g", fmt.Sprintf("%d", gopSize))
		args = append(args, "-keyint_min", fmt.Sprintf("%d", gopSize))
	}

	// Output flags from config (e.g. "-an -f mpegts")
	args = append(args, strings.Fields(cfg.Output.ExtraFlags)...)

	// Structured progress to stdout (key=value lines), suppress default stats on stderr
	args = append(args, "-nostats", "-progress", "pipe:1")
	args = append(args, udpDest)

	// Create the command with hidden console on Windows
	cmd := recording.CreateHiddenCmd(ffmpegPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stream.stdin = stdin

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

	go monitorFFmpegProgress(stream, stdout)
	go monitorFFmpegErrors(stream, stderr)
	go func() {
		err := cmd.Wait()
		wasStopping := stream.isStopping()
		stream.clearProcessHandles(cmd)
		if err != nil {
			lastErr := stream.getLastStderr()
			if wasStopping {
				if lastErr != "" {
					logging.InfoLogger.Printf("ffmpeg stopped for %s (%s): %v | last stderr: %s", stream.camera.Name, stream.udpDest, err, lastErr)
				} else {
					logging.InfoLogger.Printf("ffmpeg stopped for %s (%s): %v", stream.camera.Name, stream.udpDest, err)
				}
			} else {
				if lastErr != "" {
					logging.ErrorLogger.Printf("ffmpeg exited for %s (%s): %v | last stderr: %s", stream.camera.Name, stream.udpDest, err, lastErr)
				} else {
					logging.ErrorLogger.Printf("ffmpeg exited for %s (%s): %v", stream.camera.Name, stream.udpDest, err)
				}
			}
			stream.setStopped(fmt.Sprintf("stopped: %v", err))
			return
		}
		stream.setStopped("stopped")
	}()

	return cmd, nil
}

// stopProcess gracefully stops a camera ffmpeg process.
func stopProcess(stream *cameraStream) {
	if stream == nil {
		return
	}

	stream.markStopping()

	stream.mu.Lock()
	cmd := stream.cmd
	stdin := stream.stdin
	stream.stdin = nil
	stream.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}

	if stdin != nil {
		if err := recording.RequestFFmpegQuit(stdin); err != nil {
			logging.InfoLogger.Printf("Could not write 'q' to ffmpeg for %s (%s): %v", stream.camera.Name, stream.udpDest, err)
		}
		if err := recording.CloseFFmpegStdin(stdin); err != nil {
			logging.InfoLogger.Printf("Could not close ffmpeg stdin for %s (%s): %v", stream.camera.Name, stream.udpDest, err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return
	}

	// Try graceful shutdown first
	if runtime.GOOS == "windows" {
		// On Windows, use taskkill
		kill := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
		_ = kill.Run()
	} else {
		// On Unix, send SIGTERM then SIGKILL
		_ = cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(150 * time.Millisecond)
		// Give it a moment then force kill
		_ = cmd.Process.Kill()
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

// monitorFFmpegProgress reads structured key=value progress from stdout (-progress pipe:1)
func monitorFFmpegProgress(stream *cameraStream, stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			stream.updateProgress(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}
}

// monitorFFmpegErrors reads stderr for error logging (skip noisy H.264 sync messages)
func monitorFFmpegErrors(stream *cameraStream, stderr io.Reader) {
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
		if strings.Contains(lower, "decode_slice_header") ||
			strings.Contains(lower, "non-existing pps") ||
			strings.Contains(lower, "no frame") ||
			strings.Contains(lower, "corrupted") {
			continue
		}
		if strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "unable") || strings.Contains(lower, "invalid") || strings.Contains(lower, "permission denied") || strings.Contains(lower, "device or resource busy") {
			logging.ErrorLogger.Printf("ffmpeg stderr [%s]: %s", stream.camera.Name, line)
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
	s.stopping = false
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

func (s *cameraStream) markStopping() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopping = true
}

func (s *cameraStream) isStopping() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stopping
}

func (s *cameraStream) clearProcessHandles(cmd *exec.Cmd) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == cmd {
		s.cmd = nil
	}
	s.stdin = nil
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

func (s *cameraStream) updateProgress(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch key {
	case "frame":
		s.frame = value
		if frameNumber, err := strconv.ParseInt(value, 10, 64); err == nil {
			s.progressFrame = frameNumber
			s.hasProgressFrame = true
		}
	case "out_time":
		// out_time is always HH:MM:SS.ffffff — unambiguous across ffmpeg versions
		if us, ok := parseOutTime(value); ok {
			s.progressOutTimeUS = us
			s.hasProgressOutTime = true
		}
	case "out_time_ms", "out_time_us":
		// Fallback: if out_time wasn't seen yet, try the numeric fields.
		// out_time_ms is µs despite the name in most ffmpeg versions.
		if !s.hasProgressOutTime {
			if micros, err := strconv.ParseInt(value, 10, 64); err == nil {
				s.progressOutTimeUS = micros
				s.hasProgressOutTime = true
			}
		}
	case "fps":
		// Keep ffmpeg-reported fps as fallback, prefer computed progress-delta FPS.
		if isUsableFPSValue(value) {
			s.fps = value
			s.metricName = "FPS"
		}
	case "bitrate":
		s.bitrate = value
	case "speed":
		// Keep ffmpeg speed as fallback, prefer computed drift ratio.
		if normalizedSpeed, ok := normalizeSpeedValue(value); ok {
			s.speed = normalizedSpeed
		}
	case "progress":
		progressWallTime := time.Now()
		if s.hasProgressFrame && s.hasProgressOutTime {
			currentProgress := Progress{
				Frame:     s.progressFrame,
				OutTimeUS: s.progressOutTimeUS,
				WallTime:  progressWallTime,
			}

			if s.hasLastProgress {
				metrics := ComputeMetrics(s.lastProgress, currentProgress)

				if metrics.FPS > 0 {
					if !s.hasFPSEMA {
						s.fpsEMA = metrics.FPS
						s.hasFPSEMA = true
					} else {
						s.fpsEMA = (0.8 * s.fpsEMA) + (0.2 * metrics.FPS)
					}
					s.fps = fmt.Sprintf("%.2f", s.fpsEMA)
					s.metricName = "FPS"
				}

				if metrics.Drift > 0 {
					if !s.hasDriftEMA {
						s.driftEMA = metrics.Drift
						s.hasDriftEMA = true
					} else {
						s.driftEMA = (0.8 * s.driftEMA) + (0.2 * metrics.Drift)
					}
					if normalizedDrift, ok := formatRatioValue(s.driftEMA); ok {
						s.speed = normalizedDrift
						s.metricName = "speed"
					}
				}
			}

			s.lastProgress = currentProgress
			s.hasLastProgress = true
		}
		// Reset per-block flags so out_time takes priority in next block
		s.hasProgressOutTime = false
		s.hasProgressFrame = false
		s.running = true
		s.status = "running"
		s.lastUpdate = progressWallTime
	}
}

// parseOutTime parses ffmpeg's out_time field "HH:MM:SS.ffffff" to microseconds.
func parseOutTime(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == "N/A" {
		return 0, false
	}

	// Split "HH:MM:SS.ffffff" into time part and fractional seconds
	parts := strings.SplitN(value, ":", 3)
	if len(parts) != 3 {
		return 0, false
	}
	hours, err1 := strconv.Atoi(parts[0])
	minutes, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, false
	}

	// parts[2] is "SS.ffffff" or "SS"
	secParts := strings.SplitN(parts[2], ".", 2)
	seconds, err3 := strconv.Atoi(secParts[0])
	if err3 != nil {
		return 0, false
	}

	totalUS := int64(hours)*3_600_000_000 + int64(minutes)*60_000_000 + int64(seconds)*1_000_000

	if len(secParts) == 2 {
		frac := secParts[1]
		// Pad or truncate to 6 digits (microseconds)
		for len(frac) < 6 {
			frac += "0"
		}
		frac = frac[:6]
		fracUS, err := strconv.Atoi(frac)
		if err != nil {
			return 0, false
		}
		totalUS += int64(fracUS)
	}

	return totalUS, true
}

func isUsableFPSValue(value string) bool {
	if value == "" || value == "-" || value == "0" || value == "0.0" || value == "0.00" || value == "N/A" {
		return false
	}
	if strings.HasSuffix(value, "x") {
		return false
	}
	return true
}

func isReasonableSpeedValue(value string) bool {
	_, ok := normalizeSpeedValue(value)
	return ok
}

func formatRatioValue(ratio float64) (string, bool) {
	if !(ratio > 0 && ratio <= 10.0) {
		return "", false
	}
	return fmt.Sprintf("%.2fx", ratio), true
}

func normalizeSpeedValue(value string) (string, bool) {
	if value == "" || value == "-" || value == "N/A" {
		return "", false
	}
	if !strings.HasSuffix(value, "x") {
		return "", false
	}
	raw := strings.TrimSuffix(value, "x")
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return "", false
	}
	return formatRatioValue(n)
}

func formatFPSValue(raw string) string {
	if raw == "" || raw == "-" {
		return raw
	}
	// Speed values like "1.04x" — show as-is
	if strings.HasSuffix(raw, "x") {
		return raw
	}
	if !strings.Contains(raw, ".") {
		return raw
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return raw
	}
	return fmt.Sprintf("%.0f", value)
}

func formatSpeedValue(speed, fps string) string {
	if normalizedSpeed, ok := normalizeSpeedValue(speed); ok {
		return normalizedSpeed
	}
	if normalizedSpeed, ok := normalizeSpeedValue(fps); ok {
		return normalizedSpeed
	}
	return "-"
}

func formatMeasuredFPSValue(raw string) string {
	if !isUsableFPSValue(raw) {
		return "-"
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return raw
	}
	return fmt.Sprintf("%.2f", value)
}

func formatDriftValue(raw string) string {
	if normalizedSpeed, ok := normalizeSpeedValue(raw); ok {
		return normalizedSpeed
	}
	return "-"
}

func formatExpectedFPSValue(expected int) string {
	if expected <= 0 {
		return "-"
	}
	return strconv.Itoa(expected)
}

func multicastPort(port int) string {
	if port <= 0 {
		return "-"
	}
	return strconv.Itoa(port)
}

func (s *cameraStream) snapshotRow() [10]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return [10]string{
		s.camera.Name,
		s.camera.PixFmt,
		s.camera.Size,
		formatExpectedFPSValue(s.camera.Fps),
		formatMeasuredFPSValue(s.fps),
		formatDriftValue(formatSpeedValue(s.speed, s.fps)),
		multicastPort(s.port),
		"Preview",
		"Record 10s",
		s.status,
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
		stopProcess(&cameraStream{cmd: cmd})
	}
}

func launchPreview(stream *cameraStream, onDone func()) error {
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
		if onDone != nil {
			onDone()
		}
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
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s_%s.mp4", cameraName, timestamp))
}

// openFile opens a file with the OS default application and calls onDone when the viewer exits.
func openFile(path string, onDone func()) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		logging.ErrorLogger.Printf("Failed to open file %s: %v", path, err)
		return
	}
	go func() {
		_ = cmd.Wait()
		if onDone != nil {
			onDone()
		}
	}()
}

func recordClip(stream *cameraStream) (string, error) {
	outputPath := buildClipPath(stream)
	args := []string{
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-y",
		"-t", "10",
		"-i", stream.udpDest,
		"-c", "copy",
		"-movflags", "+faststart",
		outputPath,
	}

	cmd := recording.CreateHiddenCmd(config.GetFFmpegPath(), args...)
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return outputPath, nil
}

func runUI(streams []*cameraStream, cameras []recording.DetectedCamera, encoder *recording.HwEncoder) {
	myApp := app.New()
	setAppIcon(myApp)
	window := myApp.NewWindow("Camera Multicast Streams")
	window.Resize(fyne.NewSize(1280, 500))

	headers := []string{"Camera", "Format", "Resolution", "Expected FPS", "Measured FPS", "Drift", "Port", "Preview", "Record", "Status"}
	actionStatus := widget.NewLabel("Preview/Record: ready")
	clipLink := widget.NewHyperlink("", nil)
	clipLink.Hide()

	var clipLinkTimer *time.Timer
	scheduleClipHide := func(d time.Duration) {
		if clipLinkTimer != nil {
			clipLinkTimer.Stop()
		}
		clipLinkTimer = time.AfterFunc(d, func() {
			clipLink.Hide()
			actionStatus.SetText("Preview/Record: ready")
		})
	}

	// Starting port entry + restart button
	ipEntry := widget.NewEntry()
	ipEntry.SetText(camerasConfig.Multicast.IP)
	ipEntry.Validator = func(s string) error {
		if net.ParseIP(strings.TrimSpace(s)) == nil {
			return fmt.Errorf("must be a valid multicast IP")
		}
		return nil
	}
	ipEntryField := container.NewGridWrap(
		fyne.NewSize(180, ipEntry.MinSize().Height),
		ipEntry,
	)

	portEntry := widget.NewEntry()
	portEntry.SetText(strconv.Itoa(camerasConfig.Multicast.StartPort))
	portEntry.Validator = func(s string) error {
		if _, err := strconv.Atoi(s); err != nil {
			return fmt.Errorf("must be a number")
		}
		return nil
	}
	portEntryField := container.NewGridWrap(
		fyne.NewSize(140, portEntry.MinSize().Height),
		portEntry,
	)
	localOnlyCheck := widget.NewCheck("Local only", nil)
	localOnlyCheck.SetChecked(camerasConfig.Multicast.LocalOnly)

	// We need a mutable reference so the table and restart can update it
	currentStreams := &streams

	restartBtn := widget.NewButton("Restart Streams", nil)
	saveBtn := widget.NewButton("Save", nil)

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
			if id.Col == 7 {
				label.Hide()
				button.Show()
				button.SetText("Preview")
				stream := ss[id.Row-1]
				button.OnTapped = func() {
					if clipLinkTimer != nil {
						clipLinkTimer.Stop()
					}
					clipLink.Hide()
					actionStatus.SetText("Preview/Record: ready")
					if err := launchPreview(stream, func() {
						actionStatus.SetText("Preview/Record: ready")
					}); err != nil {
						actionStatus.SetText(fmt.Sprintf("Preview failed: %v", err))
						logging.ErrorLogger.Printf("Failed to start ffplay preview for %s (%s): %v", stream.camera.Name, stream.udpDest, err)
						return
					}
					actionStatus.SetText(fmt.Sprintf("Previewing: %s (%s)", stream.camera.Name, stream.camera.Size))
				}
				return
			}

			if id.Col == 8 {
				label.Hide()
				button.Show()
				button.SetText("Record 10s")
				stream := ss[id.Row-1]
				button.OnTapped = func() {
					if clipLinkTimer != nil {
						clipLinkTimer.Stop()
					}
					clipLink.Hide()
					actionStatus.SetText(fmt.Sprintf("Recording 10s: %s", stream.camera.Name))
					go func(s *cameraStream) {
						outputPath, err := recordClip(s)
						if err != nil {
							actionStatus.SetText(fmt.Sprintf("Record failed: %v", err))
							logging.ErrorLogger.Printf("Failed to record clip for %s (%s): %v", s.camera.Name, s.udpDest, err)
							return
						}
						path := outputPath
						actionStatus.SetText("Saved: ")
						clipLink.SetText(filepath.FromSlash(path))
						clipLink.OnTapped = func() {
							scheduleClipHide(1 * time.Minute)
							openFile(path, func() {
								if clipLinkTimer != nil {
									clipLinkTimer.Stop()
								}
								clipLink.Hide()
								actionStatus.SetText("Preview/Record: ready")
							})
						}
						clipLink.Show()
						scheduleClipHide(2 * time.Minute)
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
	table.SetColumnWidth(2, 110)
	table.SetColumnWidth(3, 100)
	table.SetColumnWidth(4, 105)
	table.SetColumnWidth(5, 80)
	table.SetColumnWidth(6, 60)
	table.SetColumnWidth(7, 90)
	table.SetColumnWidth(8, 110)
	table.SetColumnWidth(9, 240)

	stopAll := func() {
		stopAllPreviews()
		for _, stream := range *currentStreams {
			stopProcess(stream)
		}
	}

	// Wire up the restart button
	restartBtn.OnTapped = func() {
		newIP := strings.TrimSpace(ipEntry.Text)
		parsedIP := net.ParseIP(newIP)
		if parsedIP == nil || !parsedIP.IsMulticast() {
			actionStatus.SetText("Invalid multicast IP")
			return
		}

		newPort, err := strconv.Atoi(strings.TrimSpace(portEntry.Text))
		if err != nil || newPort < 1 || newPort > 65535 {
			actionStatus.SetText("Invalid starting port")
			return
		}
		actionStatus.SetText("Restarting streams...")

		// Stop existing streams
		stopAllPreviews()
		for _, stream := range *currentStreams {
			stopProcess(stream)
		}
		time.Sleep(500 * time.Millisecond) // let processes exit

		// Update config and restart
		camerasConfig.Multicast.IP = newIP
		camerasConfig.Multicast.StartPort = newPort
		camerasConfig.Multicast.LocalOnly = localOnlyCheck.Checked
		newStreams := startAllStreams(cameras, encoder)

		*currentStreams = newStreams
		table.Refresh()
		actionStatus.SetText(fmt.Sprintf("%d stream(s) restarted on port %d+", len(newStreams), newPort))
	}

	// Save current starting port to cameras.toml if it was loaded from a file.
	// If config is from embedded defaults, saving fails and is ignored.
	saveBtn.OnTapped = func() {
		newIP := strings.TrimSpace(ipEntry.Text)
		parsedIP := net.ParseIP(newIP)
		if parsedIP == nil || !parsedIP.IsMulticast() {
			actionStatus.SetText("Invalid multicast IP")
			return
		}

		newPort, err := strconv.Atoi(strings.TrimSpace(portEntry.Text))
		if err != nil || newPort < 1 || newPort > 65535 {
			actionStatus.SetText("Invalid starting port")
			return
		}

		camerasConfig.Multicast.IP = newIP
		camerasConfig.Multicast.StartPort = newPort
		camerasConfig.Multicast.LocalOnly = localOnlyCheck.Checked
		_ = config.SaveCamerasMulticastSettings(newIP, newPort, localOnlyCheck.Checked)
		if localOnlyCheck.Checked {
			actionStatus.SetText(fmt.Sprintf("Multicast %s:%d (Local only enabled)", newIP, newPort))
		} else {
			actionStatus.SetText(fmt.Sprintf("Multicast %s:%d", newIP, newPort))
		}
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
		widget.NewLabel("Multicast IP:"),
		ipEntryField,
		widget.NewLabel("Starting port:"),
		portEntryField,
		localOnlyCheck,
		saveBtn,
		restartBtn,
	)

	content := container.NewBorder(
		container.NewVBox(
			widget.NewLabel("Live stream stats (Expected FPS from detection, Measured FPS + Drift from progress deltas)"),
			portRow,
			container.NewHBox(actionStatus, clipLink),
		),
		nil,
		nil,
		nil,
		table,
	)

	window.SetContent(content)
	window.ShowAndRun()
}
