package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"

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
	cmd     *exec.Cmd
	encoder *recording.HwEncoder
}

func main() {
	// Parse command-line flags
	flag.BoolVar(&includeAll, "all", false, "Include all cameras, including raw formats (typically integrated cameras)")
	flag.IntVar(&startPort, "startport", 9001, "Starting port for multicast allocation")
	flag.Parse()

	// Initialize logging to current directory
	if err := logging.Init("."); err != nil {
		fmt.Printf("Warning: Failed to initialize logging: %v\n", err)
		// Continue anyway - we can still use fmt.Printf
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
		stream := &cameraStream{
			camera:  cam,
			port:    port,
			encoder: bestEncoder,
		}

		fmt.Printf("\n[%s] %s (%s, %s @ %d fps)\n", cam.PixFmt, cam.Name, cam.Size, cam.PixFmt, cam.Fps)
		fmt.Printf("  -> udp://%s:%d\n", baseMulticastIP, port)

		cmd, err := startStream(cam, port, bestEncoder)
		if err != nil {
			fmt.Printf("  ERROR: Failed to start stream: %v\n", err)
		} else {
			stream.cmd = cmd
			streams = append(streams, stream)
		}

		port++
	}

	if len(streams) == 0 {
		fmt.Println("\nNo streams started successfully.")
		return
	}

	fmt.Printf("\n%d camera(s) streaming. Press Ctrl+C to stop.\n", len(streams))

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\n\nStopping streams...")

	// Stop all ffmpeg processes
	for _, stream := range streams {
		if stream.cmd != nil && stream.cmd.Process != nil {
			fmt.Printf("Stopping stream for %s...\n", stream.camera.Name)
			stopProcess(stream.cmd)
		}
	}

	fmt.Println("Done.")
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
func startStream(cam recording.DetectedCamera, port int, encoder *recording.HwEncoder) (*exec.Cmd, error) {
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	logging.InfoLogger.Printf("Starting ffmpeg: %s %v", ffmpegPath, args)

	if err := cmd.Start(); err != nil {
		return nil, err
	}

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
	cmd.Wait()
}
