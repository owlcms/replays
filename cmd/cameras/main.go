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
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/recording"
)

var (
	includeAll bool
	startPort  int
)

const (
	baseMulticastIP = "239.255.0.1"
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
	flag.IntVar(&startPort, "startport", 9001, "Starting port for multicast allocation")
	flag.Parse()

	// Initialize logging to current directory
	if err := logging.InitWithFile(".", "cameras.log"); err != nil {
		fmt.Printf("Warning: Failed to initialize logging: %v\n", err)
		// Continue anyway - we can still use fmt.Printf
	} else {
		if wd, err := os.Getwd(); err == nil {
			fmt.Printf("Writing logs to: %s\n", filepath.Join(wd, "cameras.log"))
		}
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

	// Detect encoders for raw format cameras
	encoders := recording.DetectEncoders()
	bestEncoder := recording.PickBestEncoder(encoders)

	// Filter out integrated cameras and sort: H.264 first, then MJPEG, then others
	var filtered []recording.DetectedCamera
	for _, cam := range cameras {
		if !includeAll && isIntegratedCamera(cam) {
			fmt.Printf("Skipping integrated camera: %s (%s)\n", cam.Name, cam.PixFmt)
			continue
		}
		filtered = append(filtered, cam)
	}

	if len(filtered) == 0 {
		fmt.Println("No suitable cameras found (all are integrated cameras).")
		return
	}

	// Sort cameras: H.264 first, then MJPEG, then others
	sort.Slice(filtered, func(i, j int) bool {
		return formatPriority(filtered[i].PixFmt) > formatPriority(filtered[j].PixFmt)
	})

	// Start ffmpeg for each camera
	var streams []*cameraStream
	port := startPort

	fmt.Println("\nStarting camera streams:")
	fmt.Println("========================")

	for _, cam := range filtered {
		udpDest := fmt.Sprintf("udp://%s:%d", baseMulticastIP, port)
		stream := &cameraStream{
			camera:  cam,
			port:    port,
			encoder: bestEncoder,
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

	if len(streams) == 0 {
		fmt.Println("\nNo streams started successfully.")
		return
	}

	fmt.Printf("\n%d camera(s) streaming. Launching status window...\n", len(streams))
	runUI(streams)
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

// formatPriority returns a priority value for sorting (higher = better)
func formatPriority(pixFmt string) int {
	switch pixFmt {
	case "h264":
		return 3
	case "mjpeg":
		return 2
	default:
		return 1
	}
}

// startStream starts ffmpeg to stream a camera to multicast UDP
func startStream(stream *cameraStream) (*exec.Cmd, error) {
	cam := stream.camera
	encoder := stream.encoder
	port := stream.port

	ffmpegPath := config.GetFFmpegPath()
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}

	udpDest := fmt.Sprintf("udp://%s:%d?pkt_size=1316", baseMulticastIP, port)

	var args []string

	// Build input arguments based on platform and format
	switch cam.Format {
	case "dshow":
		args = append(args, "-f", "dshow")
		// Add pixel format for input
		switch cam.PixFmt {
		case "mjpeg":
			args = append(args, "-pixel_format", "mjpeg")
		case "h264":
			// No special pixel format needed
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
	switch cam.PixFmt {
	case "h264":
		// Already H.264 - just copy to MPEG-TS
		args = append(args, "-c:v", "copy")

	case "mjpeg":
		// Need to decode MJPEG and encode to H.264
		if encoder != nil {
			// Use hardware encoder
			args = append(args, strings.Fields(encoder.OutputParameters)...)
		} else {
			// Software fallback
			args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-b:v", "4M")
		}
		// Add GOP settings based on camera fps
		args = append(args, "-g", fmt.Sprintf("%d", cam.Fps))
		args = append(args, "-keyint_min", fmt.Sprintf("%d", cam.Fps))

	default:
		// Raw format - need to encode
		if encoder != nil {
			args = append(args, strings.Fields(encoder.OutputParameters)...)
		} else {
			args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-b:v", "4M")
		}
		args = append(args, "-g", fmt.Sprintf("%d", cam.Fps))
		args = append(args, "-keyint_min", fmt.Sprintf("%d", cam.Fps))
	}

	// Output to MPEG-TS over UDP multicast
	args = append(args, "-an") // No audio
	args = append(args, "-f", "mpegts")
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

	go monitorFFmpegStats(stream, stderr)
	go func() {
		err := cmd.Wait()
		if err != nil {
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

func (s *cameraStream) snapshotRow() [8]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	age := "-"
	if !s.lastUpdate.IsZero() {
		age = strconv.Itoa(int(time.Since(s.lastUpdate).Seconds())) + "s"
	}

	return [8]string{
		s.camera.Name,
		s.camera.PixFmt,
		s.camera.Size,
		s.udpDest,
		s.fps,
		s.frame,
		s.status,
		age,
	}
}

func runUI(streams []*cameraStream) {
	myApp := app.New()
	window := myApp.NewWindow("Camera Multicast Streams")
	window.Resize(fyne.NewSize(1200, 460))

	headers := []string{"Camera", "Format", "Size", "Multicast", "FPS", "Frame", "Status", "Last"}
	table := widget.NewTable(
		func() (int, int) {
			return len(streams) + 1, len(headers)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("template")
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			if id.Row == 0 {
				label.TextStyle = fyne.TextStyle{Bold: true}
				label.SetText(headers[id.Col])
				return
			}
			label.TextStyle = fyne.TextStyle{}
			row := streams[id.Row-1].snapshotRow()
			label.SetText(row[id.Col])
		},
	)

	table.SetColumnWidth(0, 260)
	table.SetColumnWidth(1, 80)
	table.SetColumnWidth(2, 90)
	table.SetColumnWidth(3, 250)
	table.SetColumnWidth(4, 70)
	table.SetColumnWidth(5, 70)
	table.SetColumnWidth(6, 220)
	table.SetColumnWidth(7, 60)

	stopAll := func() {
		for _, stream := range streams {
			if stream.cmd != nil && stream.cmd.Process != nil {
				stopProcess(stream.cmd)
			}
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

	content := container.NewBorder(
		widget.NewLabel("Live ffmpeg stream stats (FPS from ffmpeg stderr progress lines)"),
		nil,
		nil,
		nil,
		table,
	)

	window.SetContent(content)
	window.ShowAndRun()
}
