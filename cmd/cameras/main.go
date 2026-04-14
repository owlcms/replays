package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"image/color"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/owlcms/replays/internal/assets"
	"github.com/owlcms/replays/internal/config"
	camerascfg "github.com/owlcms/replays/internal/config/cameras"
	ffmpegcfg "github.com/owlcms/replays/internal/config/ffmpeg"
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

	// camerasConfig holds the per-instance cameras configuration (multicast, includeAll).
	camerasConfig *camerascfg.Config
	// ffmpegConfig holds the machine-specific encoder configuration (from ffmpeg.toml).
	ffmpegConfig *ffmpegcfg.Config
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

func newSectionTitle(text string) fyne.CanvasObject {
	title := canvas.NewText(text, theme.Color(theme.ColorNameForeground))
	title.TextSize = 16
	title.TextStyle = fyne.TextStyle{Bold: true}
	return title
}

func newVerticalGap(height float32) fyne.CanvasObject {
	rect := canvas.NewRectangle(color.Transparent)
	rect.SetMinSize(fyne.NewSize(1, height))
	return rect
}

type cameraStream struct {
	camera     recording.DetectedCamera
	port       int
	udpDest    string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	encoder    *recording.HwEncoder
	shortID    string
	summary    string
	sourceType string
	transport  string

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

func main() {
	// Set app identity before config resolution
	config.AppName = "cameras"

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

	if err := config.ResolveAndEnsureConfigDir(); err != nil {
		fmt.Printf("Failed to initialize config directory: %v\n", err)
		os.Exit(1)
	}

	if extractConfig {
		if p := camerascfg.ExtractDefaultConfig(); p == "" {
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

	// Initialize logging to the instance/version logs folder (next to executable)
	logDir := config.GetRuntimeDir()
	logDir = filepath.Join(logDir, "logs")
	if err := logging.InitWithFile(logDir, "cameras.log"); err != nil {
		fmt.Printf("Warning: Failed to initialize logging: %v\n", err)
	} else {
		fmt.Printf("Writing logs to: %s\n", filepath.Join(logDir, "cameras.log"))
	}

	if config.IsLocalDevRuntime() {
		if p := camerascfg.ExtractDefaultConfig(); p == "" {
			fmt.Println("Warning: Failed to ensure default camera config files")
		}
	}

	// Load ffmpeg configuration (machine-specific encoders from ffmpeg.toml)
	fc, fcErr := ffmpegcfg.LoadConfig()
	if fcErr != nil {
		fmt.Printf("Error loading ffmpeg config: %v\n", fcErr)
		fmt.Println("Using built-in defaults.")
		fc = &ffmpegcfg.Config{}
	}
	ffmpegConfig = fc

	// Load per-instance cameras configuration (multicast, includeAll)
	cfg, err := camerascfg.LoadConfig()
	if err != nil {
		fmt.Printf("Error loading cameras config: %v\n", err)
		fmt.Println("Using built-in defaults.")
		cfg = &camerascfg.Config{}
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

	fmt.Println("Launching status window...")
	runUI()
}

// startAllStreams starts streams for all configured or autodetected sources.
// Returns only the streams that started successfully.
func startAllStreams(sources []sourceSpec, encoder *recording.HwEncoder) []*cameraStream {
	unicastMode := camerasConfig.Unicast.Enabled
	var streams []*cameraStream

	if unicastMode {
		fmt.Println("\nStarting camera streams (unicast tee):")
		fmt.Println("=======================================")
	} else {
		fmt.Println("\nStarting camera streams (multicast):")
		fmt.Println("=====================================")
	}

	for _, source := range sources {
		cam := source.Camera
		port := source.OutputPort
		var udpDest string
		if unicastMode {
			udpDest = camerasConfig.Unicast.TeeOutput(port)
		} else {
			udpDest = fmt.Sprintf("udp://%s:%d", camerasConfig.Multicast.IP, port)
		}
		stream := &cameraStream{
			camera:     cam,
			port:       port,
			encoder:    encoder,
			udpDest:    udpDest,
			status:     "starting",
			running:    false,
			fps:        "-",
			frame:      "-",
			bitrate:    "-",
			speed:      "-",
			shortID:    source.ShortID,
			summary:    source.Summary,
			sourceType: source.SourceType,
			transport:  source.Transport,
		}

		fmt.Printf("\n[%s] %s (%s, %s @ %d fps)\n", cam.PixFmt, cam.Name, cam.Size, cam.PixFmt, cam.Fps)
		if unicastMode {
			for _, dest := range camerasConfig.Unicast.Destinations {
				fmt.Printf("  -> udp://%s:%d\n", dest.Address, port)
			}
		} else {
			fmt.Printf("  -> %s\n", udpDest)
		}

		cmd, err := startStream(stream)
		if err != nil {
			fmt.Printf("  ERROR: Failed to start stream: %v\n", err)
			stream.setStopped(fmt.Sprintf("failed: %v", err))
		} else {
			stream.cmd = cmd
			stream.setRunning()
			streams = append(streams, stream)
		}

	}

	return streams
}

func multicastOutputURL(multicast camerascfg.MulticastConfig, port int) string {
	url := fmt.Sprintf("udp://%s:%d?pkt_size=%d", multicast.IP, port, camerascfg.PktSize)
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

func describeEncodingPlan(cam recording.DetectedCamera, encoder *recording.HwEncoder, fc *ffmpegcfg.Config) string {
	pixFmt := strings.ToLower(strings.TrimSpace(cam.PixFmt))
	switch pixFmt {
	case "h264":
		return "copy input h264"
	case "hevc", "h265":
		return "copy input hevc"
	case "mjpeg":
		if encoder != nil {
			return fmt.Sprintf("encode mjpeg -> h264 via %s (%s)", encoder.Name, encoder.Description)
		}
		if fc != nil && strings.TrimSpace(fc.Software.OutputParameters) != "" {
			return fmt.Sprintf("encode mjpeg -> h264 via software (%s)", strings.TrimSpace(fc.Software.OutputParameters))
		}
		return "encode mjpeg -> h264 via software"
	default:
		if encoder != nil {
			return fmt.Sprintf("encode raw %s -> h264 via %s (%s)", cam.PixFmt, encoder.Name, encoder.Description)
		}
		if fc != nil && strings.TrimSpace(fc.Software.OutputParameters) != "" {
			return fmt.Sprintf("encode raw %s -> h264 via software (%s)", cam.PixFmt, strings.TrimSpace(fc.Software.OutputParameters))
		}
		return fmt.Sprintf("encode raw %s -> h264 via software", cam.PixFmt)
	}
}

// startStream starts ffmpeg to stream a camera to multicast UDP
func startStream(stream *cameraStream) (*exec.Cmd, error) {
	// cameras app behavior
	// - input
	//   - input format is obtained by probing cameras
	//   - format preference is defined in [cameras] priorities
	// - output
	//   - H.264 input is copied (no re-encode)
	//   - MJPEG and raw inputs are encoded to H.264 using encoder block settings
	cam := stream.camera
	encoder := stream.encoder
	port := stream.port
	camCfg := camerasConfig
	fc := ffmpegConfig

	ffmpegPath := config.GetFFmpegPath()
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}

	var udpDest string
	unicastMode := camCfg.Unicast.Enabled
	if unicastMode {
		udpDest = camCfg.Unicast.TeeOutput(port)
	} else {
		udpDest = multicastOutputURL(camCfg.Multicast, port)
	}

	var args []string

	// Some UVC cameras (especially in H.264 copy mode) produce duplicate DTS
	// values which cause the mpegts muxer inside each tee slave to fail with
	// "non monotonically increasing dts".  Using the system wall clock for
	// timestamps guarantees strict monotonicity for live device capture.
	args = append(args, "-use_wallclock_as_timestamps", "1")

	// Determine if we need hardware encoding (not h264 copy mode)
	needsEncoding := strings.ToLower(strings.TrimSpace(cam.PixFmt)) != "h264"

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
			// dshow cannot reliably negotiate H.264 input on many UVC cameras;
			// omitting -vcodec lets the device deliver its native stream.
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
		if runtime.GOOS == "windows" {
			args = append(args, "-map", "0:v:0", "-dn")
		}

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

	case "rtsp":
		transport := strings.ToLower(strings.TrimSpace(stream.transport))
		if sourceTransport := strings.ToLower(strings.TrimSpace(transport)); sourceTransport == "udp" || sourceTransport == "tcp" {
			args = append(args, "-rtsp_transport", sourceTransport)
		}
		args = append(args, "-i", cam.Device)
	}

	// Build output arguments based on input format
	gopFPS := cam.Fps
	if gopFPS <= 0 {
		gopFPS = 60
	}
	gopSize := gopFPS * fc.Output.GopMultiplier

	switch cam.PixFmt {
	case "h264":
		// Camera already outputs H.264 — just remux, no re-encode needed.
		args = append(args, "-c:v", "copy")
		// RTSP sources may deliver H.264 in AVCC (MP4-style) format; MPEG-TS
		// requires Annex B. The bitstream filter converts transparently and is
		// a no-op when the stream is already in Annex B format.
		if cam.Format == "rtsp" {
			args = append(args, "-bsf:v", "h264_mp4toannexb")
		}
	case "hevc", "h265":
		args = append(args, "-c:v", "copy")
		if cam.Format == "rtsp" {
			args = append(args, "-bsf:v", "hevc_mp4toannexb")
		}

	case "mjpeg":
		// Need to decode MJPEG and encode to H.264
		if encoder != nil {
			if strings.TrimSpace(encoder.VideoFilter) != "" {
				args = append(args, "-vf", strings.TrimSpace(encoder.VideoFilter))
			}
			args = append(args, strings.Fields(encoder.OutputParameters)...)
		} else {
			args = append(args, strings.Fields(fc.Software.OutputParameters)...)
		}
		args = append(args, "-g", fmt.Sprintf("%d", gopSize))
		args = append(args, "-keyint_min", fmt.Sprintf("%d", gopSize))

	default:
		// Raw format - need to encode
		if encoder != nil {
			if strings.TrimSpace(encoder.VideoFilter) != "" {
				args = append(args, "-vf", strings.TrimSpace(encoder.VideoFilter))
			}
			args = append(args, strings.Fields(encoder.OutputParameters)...)
		} else {
			args = append(args, strings.Fields(fc.Software.OutputParameters)...)
		}
		args = append(args, "-g", fmt.Sprintf("%d", gopSize))
		args = append(args, "-keyint_min", fmt.Sprintf("%d", gopSize))
	}

	if unicastMode {
		// Unicast tee: strip "-f mpegts" from ExtraFlags (each tee leg carries its own f=mpegts)
		extra := fc.Output.ExtraFlags
		extra = strings.ReplaceAll(extra, "-f mpegts", "")
		extra = strings.TrimSpace(extra)
		if extra != "" {
			args = append(args, strings.Fields(extra)...)
		}
		// Newer ffmpeg requires explicit stream mapping for the tee muxer
		args = append(args, "-map", "0:v")
		// Structured progress to stdout, suppress default stats on stderr
		args = append(args, "-nostats", "-progress", "pipe:1")
		args = append(args, "-f", "tee", udpDest)
	} else {
		// Output flags from config (e.g. "-an -f mpegts")
		args = append(args, strings.Fields(fc.Output.ExtraFlags)...)
		// Structured progress to stdout (key=value lines), suppress default stats on stderr
		args = append(args, "-nostats", "-progress", "pipe:1")
		args = append(args, udpDest)
	}

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

	logging.InfoLogger.Printf(
		"Stream plan for %s: source=%s format=%s %s@%dfps, %s, destination=%s",
		stream.camera.Name,
		stream.camera.Device,
		stream.camera.Format,
		stream.camera.Size,
		stream.camera.Fps,
		describeEncodingPlan(stream.camera, encoder, fc),
		stream.udpDest,
	)
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

	name := s.camera.Name
	if strings.TrimSpace(s.shortID) != "" {
		name = fmt.Sprintf("%s [%s]", name, s.shortID)
	}

	return [10]string{
		name,
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
	if envPath := strings.TrimSpace(os.Getenv("VIDEO_FFPLAY_PATH")); envPath != "" {
		return envPath
	}

	ffmpegPath := config.GetFFmpegPath()
	if ffmpegPath == "" {
		ffplayName := "ffplay"
		if runtime.GOOS == "windows" {
			ffplayName = "ffplay.exe"
		}
		if sharedPath := config.FindSharedFFmpegExecutable(ffplayName); sharedPath != "" {
			return sharedPath
		}
		return ffplayName
	}

	ffplayName := "ffplay"
	if runtime.GOOS == "windows" {
		ffplayName = "ffplay.exe"
	}

	candidate := filepath.Join(filepath.Dir(ffmpegPath), ffplayName)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}

	if sharedPath := config.FindSharedFFmpegExecutable(ffplayName); sharedPath != "" {
		return sharedPath
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

// listenURL returns a UDP URL suitable for receiving (listening to) the stream.
// In multicast mode it returns the multicast group address; in unicast mode
// it returns udp://127.0.0.1:<port> so that ffplay / ffmpeg can listen on the
// localhost copy that the tee muxer sends.
func (s *cameraStream) listenURL() string {
	if camerasConfig.Unicast.Enabled {
		return fmt.Sprintf("udp://127.0.0.1:%d", s.port)
	}
	return s.udpDest
}

func launchPreview(stream *cameraStream, onDone func()) error {
	args := []string{"-fflags", "nobuffer", "-flags", "low_delay"}
	if width, height, ok := parseResolution(stream.camera.Size); ok {
		args = append(args, "-x", strconv.Itoa(width), "-y", strconv.Itoa(height))
	}
	args = append(args, stream.listenURL())

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
	clipInput := stream.listenURL()
	if runtime.GOOS == "windows" {
		parsed, err := url.Parse(clipInput)
		if err == nil {
			query := parsed.Query()
			query.Del("pkt_size")
			if query.Get("overrun_nonfatal") == "" {
				query.Set("overrun_nonfatal", "1")
			}
			if query.Get("fifo_size") == "" {
				query.Set("fifo_size", "50000")
			}
			parsed.RawQuery = query.Encode()
			clipInput = parsed.String()
		}
	}

	outputPath := buildClipPath(stream)
	args := []string{
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-y",
		"-t", "10",
		"-i", clipInput,
		"-c", "copy",
		"-movflags", "+faststart",
		outputPath,
	}

	cmd := recording.CreateHiddenCmd(config.GetFFmpegPath(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderrText := strings.TrimSpace(stderr.String())
		if stderrText != "" {
			return "", fmt.Errorf("%w: %s", err, stderrText)
		}
		return "", err
	}
	return outputPath, nil
}

type appTheme struct{ fyne.Theme }

var (
	borderedCheckButtonIcon = fyne.NewStaticResource("checkbutton-bordered.svg", []byte(`<svg width="24" height="24" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
	<rect x="4.25" y="4.25" width="15.5" height="15.5" rx="2" fill="none" stroke="#4f4f4f" stroke-width="1.8"/>
</svg>`))
	borderedCheckButtonCheckedIcon = fyne.NewStaticResource("checkbutton-bordered-checked.svg", []byte(`<svg width="24" height="24" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
	<rect x="4.25" y="4.25" width="15.5" height="15.5" rx="2" fill="none" stroke="#4f4f4f" stroke-width="1.8"/>
  <path d="M8 12.5L10.7 15.2L16.5 9.4" fill="none" stroke="#1f6feb" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`))
	borderedCheckButtonFillIcon = fyne.NewStaticResource("checkbutton-bordered-fill.svg", []byte(`<svg width="24" height="24" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
	<rect x="4.25" y="4.25" width="15.5" height="15.5" rx="2" fill="#dbeafe" stroke="#4f4f4f" stroke-width="1.8"/>
  <path d="M8 12.5L10.7 15.2L16.5 9.4" fill="none" stroke="#1f6feb" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`))
)

func (t appTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameButton {
		// Visible steel-blue for medium-importance buttons
		return color.RGBA{R: 185, G: 205, B: 225, A: 255}
	}
	return t.Theme.Color(name, variant)
}

func (t appTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	switch name {
	case theme.IconNameCheckButton:
		return borderedCheckButtonIcon
	case theme.IconNameCheckButtonChecked:
		return borderedCheckButtonCheckedIcon
	case theme.IconNameCheckButtonFill:
		return borderedCheckButtonFillIcon
	default:
		return t.Theme.Icon(name)
	}
}

type portTableEntry struct {
	widget.Entry
	onFocusGained func()
	onFocusLost   func(string)
}

func newPortTableEntry() *portTableEntry {
	entry := &portTableEntry{}
	entry.ExtendBaseWidget(entry)
	return entry
}

func (e *portTableEntry) FocusGained() {
	e.Entry.FocusGained()
	if e.onFocusGained != nil {
		e.onFocusGained()
	}
}

func (e *portTableEntry) FocusLost() {
	e.Entry.FocusLost()
	if e.onFocusLost != nil {
		e.onFocusLost(e.Text)
	}
}

func runUI() {
	myApp := app.New()
	myApp.Settings().SetTheme(appTheme{theme.DefaultTheme()})
	setAppIcon(myApp)
	windowTitle := "Camera Streams"
	window := myApp.NewWindow(windowTitle)
	window.SetIcon(assets.IconResource)
	window.Resize(fyne.NewSize(1480, 880))

	headers := []string{"On", "Camera", "Port", "Format", "Encoder", "Resolution", "Expected FPS", "Measured FPS", "Drift", "Preview", "Record", "Status"}
	cameraStatusLabel := widget.NewLabel("Detecting sources...")
	cameraStatusLabel.TextStyle = fyne.TextStyle{Bold: true}
	actionStatus := widget.NewLabel("")
	clipLink := widget.NewHyperlink("", nil)
	clipLink.Hide()
	var monitoringSources []sourceSpec
	var activePortEditKey string
	var portEditMu sync.RWMutex

	setActivePortEdit := func(key string) {
		portEditMu.Lock()
		activePortEditKey = key
		portEditMu.Unlock()
	}

	isPortEditing := func() bool {
		portEditMu.RLock()
		defer portEditMu.RUnlock()
		return strings.TrimSpace(activePortEditKey) != ""
	}

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

	ipEntry := widget.NewEntry()
	ipEntry.SetText(camerasConfig.Multicast.IP)
	ipEntryField := container.NewGridWrap(fyne.NewSize(170, ipEntry.MinSize().Height), ipEntry)
	modeSelect := widget.NewSelect([]string{"Multicast", "Unicast"}, nil)
	if camerasConfig.Unicast.Enabled {
		modeSelect.SetSelected("Unicast")
	} else {
		modeSelect.SetSelected("Multicast")
	}
	unicastDestinations := append([]camerascfg.UnicastDestination(nil), camerasConfig.Unicast.Destinations...)
	unicastContainer := container.NewVBox()

	var saveUnicastNow func()
	var renderUnicastRows func()
	renderUnicastRows = func() {
		unicastContainer.Objects = nil
		for i := range unicastDestinations {
			idx := i
			check := widget.NewCheck("", nil)
			check.SetChecked(unicastDestinations[idx].Enabled)
			check.OnChanged = func(v bool) { unicastDestinations[idx].Enabled = v }
			entry := widget.NewEntry()
			entry.SetText(unicastDestinations[idx].Address)
			var applyBtn *widget.Button
			applyBtn = widget.NewButton("Apply", func() {
				if saveUnicastNow != nil {
					saveUnicastNow()
				}
				applyBtn.Importance = widget.MediumImportance
				applyBtn.Refresh()
			})
			entry.OnChanged = func(v string) {
				unicastDestinations[idx].Address = v
				applyBtn.Importance = widget.HighImportance
				applyBtn.Refresh()
			}
			removeBtn := widget.NewButton("Remove", func() {
				unicastDestinations = append(unicastDestinations[:idx], unicastDestinations[idx+1:]...)
				renderUnicastRows()
			})
			unicastContainer.Add(container.NewHBox(
				check,
				container.NewGridWrap(fyne.NewSize(320, entry.MinSize().Height), entry),
				container.NewGridWrap(fyne.NewSize(80, applyBtn.MinSize().Height), applyBtn),
				container.NewGridWrap(fyne.NewSize(80, removeBtn.MinSize().Height), removeBtn),
			))
		}
		// Blank add row at the bottom
		addEntry := widget.NewEntry()
		addEntry.SetPlaceHolder("Enter destination IP or host...")
		addBtn := widget.NewButton("Add", func() {
			val := strings.TrimSpace(addEntry.Text)
			if val == "" {
				return
			}
			unicastDestinations = append(unicastDestinations, camerascfg.UnicastDestination{Address: val, Enabled: true})
			renderUnicastRows()
		})
		checkSpacer := widget.NewCheck("", nil)
		checkSpacer.Disable()
		unicastContainer.Add(container.NewHBox(
			checkSpacer,
			container.NewGridWrap(fyne.NewSize(320, addEntry.MinSize().Height), addEntry),
			container.NewGridWrap(fyne.NewSize(80, addBtn.MinSize().Height), addBtn),
		))
		unicastContainer.Refresh()
	}
	renderUnicastRows()
	localOnlyCheck := widget.NewCheck("Local only", nil)
	localOnlyCheck.SetChecked(camerasConfig.Multicast.LocalOnly)

	var streams []*cameraStream
	currentStreams := &streams
	var currentInventory sourceInventory
	var currentEncoder *recording.HwEncoder
	restartBtn := widget.NewButton("Restart Streams", nil)
	restartBtn.Importance = widget.HighImportance
	saveBtn := widget.NewButton("Apply", nil)
	refreshRTSPBtn := widget.NewButton("Refresh RTSP", nil)

	usbRowsContainer := container.NewVBox()
	rtspRowsContainer := container.NewVBox()
	var usbRows []*usbSourceRow
	var rtspRows []*rtspSourceRow
	var table *widget.Table
	var saveConfigNow func()
	var applyUIToConfig func() error
	var restartStreams func()
	var renderMonitoringSourceToggles func(sourceInventory)
	var restartWithInventory func(sourceInventory, string)
	normalizeRTSPRows := func() {
		var normalized []*rtspSourceRow
		for _, row := range rtspRows {
			if row == nil {
				continue
			}
			if row.isAddRow && row.hasContent() {
				row.isAddRow = false
			}
			if row.isAddRow {
				continue
			}
			normalized = append(normalized, row)
		}
		rtspRows = normalized
		rtspRows = append(rtspRows, newBlankRTSPSourceRow())
	}

	renderUSBRows := func() {
		objects := []fyne.CanvasObject{
			container.NewHBox(
				fixedWidth(usbEnabledWidth, widget.NewLabel("On")),
				fixedWidth(usbIdentityWidth, widget.NewLabel("Stable Identity")),
				fixedWidth(usbNameWidth, widget.NewLabel("Name")),
				fixedWidth(usbShortIDWidth, widget.NewLabel("Short ID")),
				fixedWidth(usbPortWidth, widget.NewLabel("Port")),
				fixedWidth(usbFormatWidth, widget.NewLabel("Format")),
				fixedWidth(usbProbeWidth, widget.NewLabel("")),
				fixedWidth(usbSaveWidth, widget.NewLabel("")),
			),
		}
		for i, row := range usbRows {
			index := i
			objects = append(objects, row.object(func() {
				// Probe: re-detect the camera on demand
				actionStatus.SetText("Probing USB camera...")
				probeUSBSource(usbRows[index].matchKey, func(cam *recording.DetectedCamera) {
					if cam == nil {
						dialog.ShowInformation("USB Probe", "Camera not found or not connected.", window)
						actionStatus.SetText("")
						return
					}
					msg := fmt.Sprintf("%s  %s  %d fps", cam.PixFmt, cam.Size, cam.Fps)
					dialog.ShowInformation("USB Camera Detected", msg, window)
					actionStatus.SetText(msg)
				})
			}, saveConfigNow, func() {
				restartStreams()
			}))
		}
		usbRowsContainer.Objects = objects
		usbRowsContainer.Refresh()
	}

	var renderRTSPRows func()
	renderRTSPRows = func() {
		normalizeRTSPRows()
		objects := []fyne.CanvasObject{
			container.NewHBox(
				fixedWidth(rtspEnabledWidth, widget.NewLabel("Enabled")),
				fixedWidth(rtspNameWidth, widget.NewLabel("Name")),
				fixedWidth(rtspShortIDWidth, widget.NewLabel("Short ID")),
				fixedWidth(rtspURLWidth, widget.NewLabel("RTSP URL")),
				fixedWidth(rtspPortWidth, widget.NewLabel("Port")),
				fixedWidth(rtspTransportWidth, widget.NewLabel("Transport")),
				fixedWidth(rtspSaveWidth, widget.NewLabel("")),
				fixedWidth(rtspProbeWidth, widget.NewLabel("")),
				fixedWidth(rtspRemoveWidth, widget.NewLabel("")),
			),
		}
		for i, row := range rtspRows {
			index := i
			objects = append(objects, row.object(func() {
				// Add handler
				if !rtspRows[index].hasContent() {
					return
				}
				rtspRows[index].isAddRow = false
				if !rtspRows[index].enabledCheck.Checked {
					rtspRows[index].enabledCheck.SetChecked(true)
				}
				renderRTSPRows()
				if saveConfigNow != nil {
					saveConfigNow()
				}
			}, func() {
				// Save handler for this row
				if saveConfigNow != nil {
					saveConfigNow()
				}
			}, func() {
				// Probe handler for this row
				row := rtspRows[index]
				src, skip, err := row.source()
				if skip || err != nil || strings.TrimSpace(src.RTSPURL) == "" {
					actionStatus.SetText("Enter a URL before probing")
					return
				}
				actionStatus.SetText(fmt.Sprintf("Probing %s...", summarizeRTSPURL(src.RTSPURL)))
				probeAndFillRTSPRow(src, func(codec, size string, fps int, probeErr error) {
					if probeErr != nil {
						dialog.ShowInformation("Probe Failed", probeErr.Error(), window)
						actionStatus.SetText(fmt.Sprintf("Probe failed: %v", probeErr))
						return
					}
					msg := fmt.Sprintf("%s  %s", codec, size)
					if fps > 0 {
						msg = fmt.Sprintf("%s  %s  %d fps", codec, size, fps)
					}
					dialog.ShowCustomConfirm("RTSP Probe Result", "Apply", "Cancel",
						widget.NewLabel(msg),
						func(apply bool) {
							if apply {
								row.detectedCodec = codec
								if saveConfigNow != nil {
									saveConfigNow()
								}
							}
						}, window)
					actionStatus.SetText(msg)
				})
			}, func() {
				// Remove handler
				rtspRows = append(rtspRows[:index], rtspRows[index+1:]...)
				renderRTSPRows()
				if saveConfigNow != nil {
					saveConfigNow()
				}
			}))
		}
		rtspRowsContainer.Objects = objects
		rtspRowsContainer.Refresh()
	}

	loadInventoryIntoRows := func(inv sourceInventory) {
		usbRows = usbRows[:0]
		for _, spec := range inv.USB {
			usbRows = append(usbRows, newUSBSourceRow(spec))
		}
		renderUSBRows()

		rtspRows = rtspRows[:0]
		for _, spec := range inv.RTSP {
			row := newRTSPSourceRow(spec)
			row.isAddRow = false
			rtspRows = append(rtspRows, row)
		}
		renderRTSPRows()
	}

	findInventorySource := func(key string) (*sourceSpec, bool) {
		for i := range currentInventory.USB {
			if currentInventory.USB[i].Key == key {
				return &currentInventory.USB[i], true
			}
		}
		for i := range currentInventory.RTSP {
			if currentInventory.RTSP[i].Key == key {
				return &currentInventory.RTSP[i], true
			}
		}
		return nil, false
	}

	findStreamIndex := func(key string) int {
		for i, stream := range *currentStreams {
			if stream != nil && stream.camera.MatchKey == key {
				return i
			}
		}
		return -1
	}

	refreshMonitoringStatus := func(message string) {
		if !isPortEditing() {
			table.Refresh()
		}
		status := currentInventory.Status
		if len(currentInventory.Errors) > 0 {
			status = strings.Join(currentInventory.Errors, " | ")
		}
		if running := len(*currentStreams); running > 0 {
			status = fmt.Sprintf("%d source(s) streaming.", running)
		}
		cameraStatusLabel.SetText(status)
		actionStatus.SetText(message)
	}

	startSingleSource := func(spec sourceSpec) error {
		cam := spec.Camera
		port := spec.OutputPort
		var udpDest string
		if camerasConfig.Unicast.Enabled {
			udpDest = camerasConfig.Unicast.TeeOutput(port)
		} else {
			udpDest = multicastOutputURL(camerasConfig.Multicast, port)
		}
		stream := &cameraStream{
			camera:     cam,
			port:       port,
			encoder:    currentEncoder,
			udpDest:    udpDest,
			status:     "starting",
			running:    false,
			fps:        "-",
			frame:      "-",
			bitrate:    "-",
			speed:      "-",
			shortID:    spec.ShortID,
			summary:    spec.Summary,
			sourceType: spec.SourceType,
			transport:  spec.Transport,
		}

		cmd, err := startStream(stream)
		if err != nil {
			stream.setStopped(fmt.Sprintf("failed: %v", err))
			return err
		}
		stream.cmd = cmd
		stream.setRunning()
		*currentStreams = append(*currentStreams, stream)
		sort.Slice(*currentStreams, func(i, j int) bool {
			if (*currentStreams)[i].port != (*currentStreams)[j].port {
				return (*currentStreams)[i].port < (*currentStreams)[j].port
			}
			return (*currentStreams)[i].camera.Name < (*currentStreams)[j].camera.Name
		})
		return nil
	}

	stopSingleSource := func(key string) {
		idx := findStreamIndex(key)
		if idx < 0 {
			return
		}
		stream := (*currentStreams)[idx]
		stopProcess(stream)
		updated := append([]*cameraStream(nil), (*currentStreams)[:idx]...)
		updated = append(updated, (*currentStreams)[idx+1:]...)
		*currentStreams = updated
	}

	toggleSingleSource := func(spec sourceSpec, enabled bool) error {
		item, ok := findInventorySource(spec.Key)
		if !ok {
			return fmt.Errorf("source %s not found", spec.Name)
		}
		if enabled {
			if item.OutputPort <= 0 {
				return fmt.Errorf("%s has no output port configured", item.Name)
			}
			for _, src := range monitoringSources {
				if src.Key != item.Key && src.OutputPort > 0 && src.OutputPort == item.OutputPort {
					return fmt.Errorf("port %d is already used by %s", item.OutputPort, src.Name)
				}
			}
			if findStreamIndex(spec.Key) == -1 {
				if err := startSingleSource(*item); err != nil {
					return fmt.Errorf("failed to start %s: %w", item.Name, err)
				}
			}
			refreshMonitoringStatus(fmt.Sprintf("Started %s", item.Name))
			return nil
		}
		stopSingleSource(spec.Key)
		refreshMonitoringStatus(fmt.Sprintf("Stopped %s", item.Name))
		return nil
	}

	renderMonitoringSourceToggles = func(inv sourceInventory) {
		sources := make([]sourceSpec, 0, len(inv.USB)+len(inv.RTSP))
		sources = append(sources, inv.USB...)
		sources = append(sources, inv.RTSP...)
		monitoringSources = sources
		table.Refresh()
	}

	saveConfigNow = func() {
		if err := applyUIToConfig(); err != nil {
			actionStatus.SetText(fmt.Sprintf("Auto-save failed: %v", err))
			return
		}
		if err := camerascfg.SaveConfig(camerasConfig); err != nil {
			actionStatus.SetText(fmt.Sprintf("Auto-save failed: %v", err))
			return
		}
		actionStatus.SetText("Configuration saved")
	}

	applyUIToConfig = func() error {
		selectedMode := strings.TrimSpace(modeSelect.Selected)
		if selectedMode == "Unicast" {
			var destinations []camerascfg.UnicastDestination
			for _, d := range unicastDestinations {
				if strings.TrimSpace(d.Address) != "" {
					destinations = append(destinations, d)
				}
			}
			if len(destinations) == 0 {
				return fmt.Errorf("at least one unicast destination is required")
			}
			camerasConfig.Unicast.Enabled = true
			camerasConfig.Unicast.Destinations = destinations
		} else {
			newIP := strings.TrimSpace(ipEntry.Text)
			parsedIP := net.ParseIP(newIP)
			if parsedIP == nil || !parsedIP.IsMulticast() {
				return fmt.Errorf("invalid multicast IP")
			}
			camerasConfig.Unicast.Enabled = false
			camerasConfig.Multicast.IP = newIP
			camerasConfig.Multicast.LocalOnly = localOnlyCheck.Checked
		}

		assignments := make([]camerascfg.DeviceAssignment, 0, len(usbRows))
		portOwners := make(map[int]string)
		for _, row := range usbRows {
			assignment, err := row.assignment()
			if err != nil {
				return err
			}
			if assignment.OutputPort > 0 {
				if owner, exists := portOwners[assignment.OutputPort]; exists {
					return fmt.Errorf("duplicate output port %d for %s and %s", assignment.OutputPort, owner, assignment.Name)
				}
				portOwners[assignment.OutputPort] = assignment.Name
			}
			assignments = append(assignments, assignment)
		}

		normalizeRTSPRows()
		rtspSources := make([]camerascfg.RTSPSource, 0, len(rtspRows))
		for _, row := range rtspRows {
			source, skip, err := row.source()
			if err != nil {
				return err
			}
			if skip {
				continue
			}
			if source.OutputPort > 0 {
				if owner, exists := portOwners[source.OutputPort]; exists {
					return fmt.Errorf("duplicate output port %d for %s and %s", source.OutputPort, owner, source.Name)
				}
				portOwners[source.OutputPort] = source.Name
			}
			rtspSources = append(rtspSources, source)
		}

		camerasConfig.DeviceAssignments = assignments
		camerasConfig.RTSPSources = rtspSources
		return nil
	}

	findStreamForSource := func(key string) *cameraStream {
		for _, stream := range *currentStreams {
			if stream != nil && stream.camera.MatchKey == key {
				return stream
			}
		}
		return nil
	}

	ensureDeviceAssignment := func(spec sourceSpec) *camerascfg.DeviceAssignment {
		for i := range camerasConfig.DeviceAssignments {
			if camerasConfig.DeviceAssignments[i].MatchKey == spec.Key {
				return &camerasConfig.DeviceAssignments[i]
			}
		}
		camerasConfig.DeviceAssignments = append(camerasConfig.DeviceAssignments, camerascfg.DeviceAssignment{
			MatchKey: spec.Key,
			Name:     spec.Name,
			ShortID:  spec.ShortID,
		})
		return &camerasConfig.DeviceAssignments[len(camerasConfig.DeviceAssignments)-1]
	}

	updateSourcePort := func(spec sourceSpec, newPort int) error {
		// Check for duplicate port
		for _, src := range monitoringSources {
			if newPort > 0 && src.Key != spec.Key && src.OutputPort == newPort {
				return fmt.Errorf("port %d is already used by %s", newPort, src.Name)
			}
		}
		// Update inventory
		item, ok := findInventorySource(spec.Key)
		if !ok {
			return fmt.Errorf("source %s not found", spec.Name)
		}
		item.OutputPort = newPort
		// Update config and save
		switch spec.SourceType {
		case "usb":
			assignment := ensureDeviceAssignment(spec)
			assignment.OutputPort = newPort
		case "rtsp":
			for i := range camerasConfig.RTSPSources {
				if camerasConfig.RTSPSources[i].SourceID == spec.Key {
					camerasConfig.RTSPSources[i].OutputPort = newPort
					break
				}
			}
		}
		if err := camerascfg.SaveConfig(camerasConfig); err != nil {
			return fmt.Errorf("save failed: %w", err)
		}
		// Restart stream if running, or stop it if the port was cleared.
		if findStreamIndex(spec.Key) >= 0 {
			stopSingleSource(spec.Key)
			if newPort > 0 {
				if err := startSingleSource(*item); err != nil {
					return fmt.Errorf("restart failed for %s: %w", item.Name, err)
				}
				actionStatus.SetText(fmt.Sprintf("Port changed to %d for %s", newPort, item.Name))
			} else {
				actionStatus.SetText(fmt.Sprintf("Cleared port for %s; stream stopped", item.Name))
			}
			renderMonitoringSourceToggles(currentInventory)
			loadInventoryIntoRows(currentInventory)
			return nil
		} else {
			if newPort > 0 {
				actionStatus.SetText(fmt.Sprintf("Port changed to %d for %s", newPort, item.Name))
			} else {
				actionStatus.SetText(fmt.Sprintf("Cleared port for %s", item.Name))
			}
		}
		renderMonitoringSourceToggles(currentInventory)
		loadInventoryIntoRows(currentInventory)
		return nil
	}

	commitSourcePort := func(spec sourceSpec, entry *portTableEntry, value string) {
		setActivePortEdit("")
		item, ok := findInventorySource(spec.Key)
		if !ok {
			return
		}
		trimmed := strings.TrimSpace(value)
		newPort := 0
		var err error
		if trimmed != "" {
			newPort, err = strconv.Atoi(trimmed)
		}
		if err != nil || newPort < 0 || newPort > 65535 {
			dialog.ShowError(fmt.Errorf("Invalid port number: %s", trimmed), window)
			entry.SetText(multicastPort(item.OutputPort))
			return
		}
		if newPort == item.OutputPort {
			entry.SetText(multicastPort(item.OutputPort))
			return
		}
		if err := updateSourcePort(spec, newPort); err != nil {
			dialog.ShowError(err, window)
			entry.SetText(multicastPort(item.OutputPort))
			return
		}
	}

	table = widget.NewTable(
		func() (int, int) {
			return len(monitoringSources) + 1, len(headers)
		},
		func() fyne.CanvasObject {
			label := widget.NewLabel("template")
			button := widget.NewButton("Preview", nil)
			button.Hide()
			check := widget.NewCheck("", nil)
			check.Hide()
			entry := newPortTableEntry()
			entry.Hide()
			return container.NewMax(label, button, check, entry)
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			cell := obj.(*fyne.Container)
			label := cell.Objects[0].(*widget.Label)
			button := cell.Objects[1].(*widget.Button)
			check := cell.Objects[2].(*widget.Check)
			entry := cell.Objects[3].(*portTableEntry)

			if id.Row == 0 {
				button.Hide()
				check.Hide()
				entry.Hide()
				label.TextStyle = fyne.TextStyle{Bold: true}
				label.Show()
				label.SetText(headers[id.Col])
				return
			}

			srcIdx := id.Row - 1
			if srcIdx >= len(monitoringSources) {
				button.Hide()
				check.Hide()
				entry.Hide()
				label.Show()
				label.SetText("")
				return
			}
			spec := monitoringSources[srcIdx]
			stream := findStreamForSource(spec.Key)

			// Column 0: enabled checkbox (reflects whether ffmpeg is running)
			if id.Col == 0 {
				label.Hide()
				button.Hide()
				entry.Hide()
				check.Show()
				check.OnChanged = nil
				check.SetChecked(stream != nil)
				check.OnChanged = func(enabled bool) {
					actionStatus.SetText(fmt.Sprintf("Updating %s...", spec.Name))
					if err := toggleSingleSource(spec, enabled); err != nil {
						dialog.ShowError(err, window)
						actionStatus.SetText(fmt.Sprintf("Update failed: %v", err))
						table.Refresh()
					}
				}
				return
			}

			// Column 2: editable port
			if id.Col == 2 {
				label.Hide()
				button.Hide()
				check.Hide()
				entry.Show()
				entry.onFocusGained = nil
				entry.onFocusLost = nil
				entry.OnSubmitted = nil
				if !isPortEditing() || strings.TrimSpace(activePortEditKey) != spec.Key {
					entry.SetText(multicastPort(spec.OutputPort))
				}
				capturedSpec := spec
				entry.onFocusGained = func() {
					setActivePortEdit(capturedSpec.Key)
				}
				entry.onFocusLost = func(val string) {
					commitSourcePort(capturedSpec, entry, val)
				}
				entry.OnSubmitted = func(val string) {
					commitSourcePort(capturedSpec, entry, val)
				}
				return
			}

			check.Hide()
			entry.Hide()

			// Build row data from stream if running, otherwise from spec
			var row [12]string
			name := spec.Name
			if strings.TrimSpace(spec.ShortID) != "" {
				name = fmt.Sprintf("%s [%s]", name, spec.ShortID)
			}
			row[1] = name
			// col 2 is the port entry, handled above
			row[3] = spec.Camera.PixFmt
			// Encoder column
			pixFmt := strings.ToLower(strings.TrimSpace(spec.Camera.PixFmt))
			switch pixFmt {
			case "h264", "hevc", "h265":
				row[4] = "copy"
			default:
				if currentEncoder != nil {
					row[4] = currentEncoder.Name
				} else {
					row[4] = "software"
				}
			}
			row[5] = spec.Camera.Size
			row[6] = formatExpectedFPSValue(spec.Camera.Fps)
			if stream != nil {
				snapshot := stream.snapshotRow()
				row[7] = snapshot[4]  // Measured FPS
				row[8] = snapshot[5]  // Drift
				row[11] = snapshot[9] // Status
			} else {
				row[11] = "stopped"
			}

			// Preview button (col 9)
			if id.Col == 9 && stream != nil {
				label.Hide()
				button.Show()
				button.SetText("Preview")
				capturedStream := stream
				button.OnTapped = func() {
					if clipLinkTimer != nil {
						clipLinkTimer.Stop()
					}
					clipLink.Hide()
					actionStatus.SetText("Preview/Record: ready")
					if err := launchPreview(capturedStream, func() {
						actionStatus.SetText("Preview/Record: ready")
					}); err != nil {
						actionStatus.SetText(fmt.Sprintf("Preview failed: %v", err))
						logging.ErrorLogger.Printf("Failed to start ffplay preview for %s (%s): %v", capturedStream.camera.Name, capturedStream.udpDest, err)
						return
					}
					actionStatus.SetText(fmt.Sprintf("Previewing: %s (%s)", capturedStream.camera.Name, capturedStream.camera.Size))
				}
				return
			}

			// Record button (col 10)
			if id.Col == 10 && stream != nil {
				label.Hide()
				button.Show()
				button.SetText("Record 10s")
				capturedStream := stream
				button.OnTapped = func() {
					if clipLinkTimer != nil {
						clipLinkTimer.Stop()
					}
					clipLink.Hide()
					actionStatus.SetText(fmt.Sprintf("Recording 10s: %s", capturedStream.camera.Name))
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
					}(capturedStream)
				}
				return
			}

			button.Hide()
			label.Show()
			label.TextStyle = fyne.TextStyle{}
			label.SetText(row[id.Col])
		},
	)

	table.SetColumnWidth(0, 40)
	table.SetColumnWidth(1, 240)
	table.SetColumnWidth(2, 65)
	table.SetColumnWidth(3, 70)
	table.SetColumnWidth(4, 100)
	table.SetColumnWidth(5, 100)
	table.SetColumnWidth(6, 100)
	table.SetColumnWidth(7, 100)
	table.SetColumnWidth(8, 70)
	table.SetColumnWidth(9, 80)
	table.SetColumnWidth(10, 100)
	table.SetColumnWidth(11, 220)

	stopAll := func() {
		stopAllPreviews()
		for _, stream := range *currentStreams {
			stopProcess(stream)
		}
	}

	restartWithInventory = func(inv sourceInventory, successMessage string) {
		actionStatus.SetText("Restarting streams...")
		stopAll()
		time.Sleep(500 * time.Millisecond)

		currentInventory = inv
		loadInventoryIntoRows(inv)
		renderMonitoringSourceToggles(inv)
		var newStreams []*cameraStream
		newEncoder := currentEncoder
		if inv.Encoder != nil {
			newEncoder = inv.Encoder
		}
		currentEncoder = newEncoder
		if len(inv.Errors) == 0 && len(inv.Active) > 0 {
			newStreams = startAllStreams(inv.Active, newEncoder)
		}
		*currentStreams = newStreams
		table.Refresh()

		status := inv.Status
		if len(inv.Errors) > 0 {
			status = strings.Join(inv.Errors, " | ")
		}
		if len(newStreams) > 0 {
			status = fmt.Sprintf("%d source(s) streaming.", len(newStreams))
		}
		cameraStatusLabel.SetText(status)
		actionStatus.SetText(successMessage)
	}

	restartStreams = func() {
		if err := applyUIToConfig(); err != nil {
			actionStatus.SetText(err.Error())
			return
		}
		restartWithInventory(buildSourceInventory(), "Streams restarted")
	}

	restartBtn.OnTapped = func() {
		restartStreams()
	}
	saveBtn.OnTapped = func() {
		if err := applyUIToConfig(); err != nil {
			actionStatus.SetText(err.Error())
			return
		}
		if err := camerascfg.SaveConfig(camerasConfig); err != nil {
			actionStatus.SetText(err.Error())
			return
		}
		saveBtn.Importance = widget.MediumImportance
		saveBtn.Refresh()
		actionStatus.SetText("Configuration saved")
	}
	refreshRTSPBtn.OnTapped = func() {
		cfg, err := camerascfg.LoadConfig()
		if err != nil {
			actionStatus.SetText(fmt.Sprintf("RTSP refresh failed: %v", err))
			return
		}
		camerasConfig.RTSPSources = append([]camerascfg.RTSPSource(nil), cfg.RTSPSources...)
		inv := buildSourceInventory()
		loadInventoryIntoRows(inv)
		actionStatus.SetText("RTSP sources reloaded from config")
	}

	// Detection and streaming happen in background after window is shown.
	go func() {
		inv := buildSourceInventory()
		if inv.Encoder != nil {
			logging.InfoLogger.Printf("Best encoder: %s (%s)", inv.Encoder.Name, inv.Encoder.Description)
		} else {
			logging.InfoLogger.Printf("No hardware encoder available, will use software: %s", ffmpegConfig.Software.OutputParameters)
		}
		if len(inv.Errors) > 0 {
			for _, item := range inv.Errors {
				logging.ErrorLogger.Println(item)
			}
		}

		currentInventory = inv
		if inv.Encoder != nil {
			currentEncoder = inv.Encoder
		}
		loadInventoryIntoRows(inv)
		renderMonitoringSourceToggles(inv)

		if len(inv.Active) > 0 && len(inv.Errors) == 0 {
			newStreams := startAllStreams(inv.Active, inv.Encoder)
			*currentStreams = newStreams
			if len(newStreams) > 0 {
				cameraStatusLabel.SetText(fmt.Sprintf("%d source(s) streaming.", len(newStreams)))
			} else {
				cameraStatusLabel.SetText("No streams started successfully. Check logs for errors.")
			}
		} else {
			cameraStatusLabel.SetText(inv.Status)
		}
		actionStatus.SetText("Preview/Record: ready")
		table.Refresh()
	}()

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
			if isPortEditing() {
				continue
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

	multicastRow := container.NewHBox(
		widget.NewLabel("Multicast IP:"),
		ipEntryField,
		localOnlyCheck,
	)
	saveUnicastNow = func() {
		var destinations []camerascfg.UnicastDestination
		for _, d := range unicastDestinations {
			if strings.TrimSpace(d.Address) != "" {
				destinations = append(destinations, d)
			}
		}
		if err := camerascfg.SaveUnicastSettings(
			strings.TrimSpace(modeSelect.Selected) == "Unicast",
			camerasConfig.Unicast.StartPort,
			destinations,
		); err != nil {
			actionStatus.SetText("Error: " + err.Error())
			return
		}
		camerasConfig.Unicast.Destinations = destinations
		actionStatus.SetText("Unicast settings saved")
	}
	unicastRow := container.NewVBox(
		widget.NewLabel("Unicast destinations:"),
		unicastContainer,
	)
	updateModeUI := func() {
		if strings.TrimSpace(modeSelect.Selected) == "Unicast" {
			multicastRow.Hide()
			unicastRow.Show()
		} else {
			unicastRow.Hide()
			multicastRow.Show()
		}
	}
	modeSelect.OnChanged = func(selected string) {
		updateModeUI()
		saveBtn.Importance = widget.HighImportance
		saveBtn.Refresh()
	}
	updateModeUI()

	portRow := container.NewHBox(
		widget.NewLabel("Mode:"),
		fixedWidth(120, modeSelect),
		saveBtn,
	)

	broadcastSection := container.NewVBox(
		newSectionTitle("Broadcast Mode"),
		newVerticalGap(6),
		portRow,
		multicastRow,
		unicastRow,
	)
	usbSection := container.NewVBox(
		newSectionTitle("Local Sources"),
		newVerticalGap(6),
		usbRowsContainer,
	)
	rtspSection := container.NewVBox(
		newSectionTitle("RTSP Sources"),
		newVerticalGap(6),
		rtspRowsContainer,
	)

	monitoringTab := container.NewBorder(
		container.NewVBox(
			cameraStatusLabel,
			container.NewHBox(actionStatus, clipLink, restartBtn),
		),
		nil,
		nil,
		nil,
		table,
	)
	saveAllBtn := widget.NewButton("Save All Settings", func() {
		if saveConfigNow != nil {
			saveConfigNow()
			actionStatus.SetText("All settings saved")
		}
	})
	saveAllBtn.Importance = widget.HighImportance
	restartFromConfigBtn := widget.NewButton("Restart Streams", func() {
		restartStreams()
	})
	restartFromConfigBtn.Importance = widget.HighImportance
	configurationTab := container.NewVScroll(
		container.NewPadded(container.NewVBox(
			newVerticalGap(4),
			container.NewHBox(saveAllBtn, restartFromConfigBtn),
			newVerticalGap(8),
			broadcastSection,
			newVerticalGap(12),
			usbSection,
			newVerticalGap(12),
			rtspSection,
			newVerticalGap(8),
		)),
	)
	configurationTab.SetMinSize(fyne.NewSize(0, 420))

	content := container.NewAppTabs(
		container.NewTabItem("Monitoring", monitoringTab),
		container.NewTabItem("Configuration", configurationTab),
	)

	window.SetContent(content)
	window.ShowAndRun()
}
