package recording

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
)

// detectedCamera holds information about a detected camera device
type detectedCamera struct {
	name   string
	device string // device path (Linux) or device name (Windows)
	format string // v4l2 or dshow
	pixFmt string // mjpeg, yuyv422, etc.
	size   string // best resolution found
	fps    int    // best fps for that resolution
}

// hwEncoder holds information about a detected hardware encoder
type hwEncoder struct {
	name            string // h264_nvenc, h264_vaapi, h264_amf, h264_qsv
	description     string
	inputParameters string
	outputParameters string
}

// DetectAndWriteConfig probes cameras and GPU encoders, then writes auto.toml
func DetectAndWriteConfig(window fyne.Window) {
	logging.InfoLogger.Println("Starting hardware auto-detection...")

	progressLabel := widget.NewLabel("Detecting hardware encoders...")
	progressDialog := dialog.NewCustomWithoutButtons("Auto-Detecting Hardware", progressLabel, window)
	progressDialog.Show()

	go func() {
		defer progressDialog.Hide()

		// Step 1: Detect available H.264 hardware encoders
		progressLabel.SetText("Detecting hardware encoders...")
		encoders := detectEncoders()
		logging.InfoLogger.Printf("Detected %d hardware encoders", len(encoders))

		// Step 2: Detect cameras
		progressLabel.SetText("Detecting cameras...")
		cameras := detectCameras()
		logging.InfoLogger.Printf("Detected %d cameras", len(cameras))

		// Step 3: Write auto.toml (even with 0 cameras, to show detected encoders)
		progressLabel.SetText("Writing auto.toml...")
		outputPath := filepath.Join(config.GetInstallDir(), "auto.toml")
		err := writeAutoConfig(outputPath, cameras, encoders)
		if err != nil {
			logging.ErrorLogger.Printf("Failed to write auto.toml: %v", err)
			dialog.ShowError(fmt.Errorf("failed to write auto.toml: %v", err), window)
			return
		}

		// Step 4: Show results
		summary := buildSummary(cameras, encoders, outputPath)
		showAutoDetectResults(summary, outputPath, window)
	}()
}

// detectEncoders probes ffmpeg for available H.264 hardware encoders
func detectEncoders() []hwEncoder {
	path := config.GetFFmpegPath()
	if path == "" {
		path = "ffmpeg"
	}

	cmd := exec.Command(path, "-hide_banner", "-encoders")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		logging.ErrorLogger.Printf("Failed to query ffmpeg encoders: %v", err)
		return nil
	}

	var candidates []hwEncoder
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		line := scanner.Text()
		// Lines look like: " V....D h264_nvenc           NVIDIA NVENC H.264 encoder (codec h264)"
		if !strings.Contains(line, "h264_") {
			continue
		}
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// The encoder name is the second field (after the flags field)
		encoderName := fields[1]

		switch encoderName {
		case "h264_nvenc":
			candidates = append(candidates, hwEncoder{
				name:            "h264_nvenc",
				description:     "NVIDIA GPU",
				inputParameters: "-rtbufsize 512M -thread_queue_size 4096",
				outputParameters: "-c:v h264_nvenc -preset p5 -rc cbr -b:v 8M -g 60 -keyint_min 60 -r 60 -vsync cfr -an",
			})
		case "h264_amf":
			candidates = append(candidates, hwEncoder{
				name:            "h264_amf",
				description:     "AMD GPU (AMF)",
				inputParameters: "-rtbufsize 512M -thread_queue_size 4096",
				outputParameters: "-c:v h264_amf -rc cbr -b:v 8M -g 60 -keyint_min 60 -r 60 -vsync cfr -an",
			})
		case "h264_vaapi":
			if runtime.GOOS == "linux" {
				candidates = append(candidates, hwEncoder{
					name:            "h264_vaapi",
					description:     "VAAPI (AMD/Intel on Linux)",
					inputParameters: "-hwaccel vaapi -vaapi_device /dev/dri/renderD128 -rtbufsize 512M -thread_queue_size 4096",
					outputParameters: "-c:v h264_vaapi -rc_mode CBR -b:v 8M -g 60 -keyint_min 60 -r 60 -vsync cfr -an",
				})
			}
		case "h264_qsv":
			candidates = append(candidates, hwEncoder{
				name:            "h264_qsv",
				description:     "Intel GPU (QSV)",
				inputParameters: "-init_hw_device qsv=hw -filter_hw_device hw -rtbufsize 512M -thread_queue_size 4096",
				outputParameters: "-c:v h264_qsv -preset medium -look_ahead 0 -rc_mode CBR -b:v 8M -g 60 -keyint_min 60 -r 60 -vsync cfr -an",
			})
		}
	}

	// Verify each candidate encoder actually works on this hardware
	var found []hwEncoder
	for _, enc := range candidates {
		if testEncoder(path, enc.name) {
			logging.InfoLogger.Printf("Encoder %s verified working", enc.name)
			found = append(found, enc)
		} else {
			logging.InfoLogger.Printf("Encoder %s compiled in but not functional on this hardware", enc.name)
		}
	}
	return found
}

// testEncoder tries to actually use an encoder to verify it works on this hardware
func testEncoder(ffmpegPath, encoderName string) bool {
	// Build a minimal encoding test: generate 1 frame of blank video and try to encode it
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "nullsrc=s=64x64:d=0.1"}

	// Some encoders need special init flags
	switch encoderName {
	case "h264_vaapi":
		args = append([]string{"-hide_banner", "-loglevel", "error",
			"-init_hw_device", "vaapi=va:/dev/dri/renderD128",
			"-f", "lavfi", "-i", "nullsrc=s=64x64:d=0.1",
			"-vf", "format=nv12,hwupload"}, // VAAPI needs hwupload
		)
		args = append(args, "-c:v", encoderName, "-f", "null", "-")
	default:
		args = append(args, "-c:v", encoderName, "-f", "null", "-")
	}

	cmd := exec.Command(ffmpegPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		logging.InfoLogger.Printf("Encoder test for %s failed: %v (%s)", encoderName, err, strings.TrimSpace(stderr.String()))
		return false
	}
	return true
}

// detectCameras detects available camera devices and their capabilities
func detectCameras() []detectedCamera {
	switch runtime.GOOS {
	case "linux":
		return detectCamerasLinux()
	case "windows":
		return detectCamerasWindows()
	default:
		return nil
	}
}

// detectCamerasLinux uses v4l2-ctl to detect cameras and their formats
func detectCamerasLinux() []detectedCamera {
	// First get the device list
	cmd := exec.Command("v4l2-ctl", "--list-devices")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		logging.ErrorLogger.Printf("v4l2-ctl --list-devices failed: %v", err)
		return nil
	}

	// Parse device list: camera name followed by indented /dev/videoN lines
	type deviceEntry struct {
		name   string
		device string
	}
	var devices []deviceEntry
	var currentName string
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") {
			// Camera name line - strip any parenthetical suffix like (usb-...) or (platform:...)
			if idx := strings.Index(line, " ("); idx != -1 {
				currentName = strings.TrimSpace(line[:idx])
			} else {
				currentName = strings.TrimRight(strings.TrimSpace(line), ":")
			}
			logging.InfoLogger.Printf("v4l2 device group: %s", currentName)
		} else {
			trimmed := strings.TrimSpace(line)
			// Skip /dev/media entries, only consider /dev/videoN
			if strings.HasPrefix(trimmed, "/dev/video") && currentName != "" {
				logging.InfoLogger.Printf("v4l2 device: %s -> %s", currentName, trimmed)
				devices = append(devices, deviceEntry{name: currentName, device: trimmed})
				currentName = "" // skip subsequent devices for same camera
			}
		}
	}

	var cameras []detectedCamera
	for _, dev := range devices {
		cam := probeV4L2Device(dev.name, dev.device)
		if cam != nil {
			cameras = append(cameras, *cam)
		}
	}
	return cameras
}

// probeV4L2Device probes a single v4l2 device for its best format
func probeV4L2Device(name, device string) *detectedCamera {
	cmd := exec.Command("v4l2-ctl", "-d", device, "--list-formats-ext")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		logging.ErrorLogger.Printf("Failed to probe %s: %v", device, err)
		return nil
	}

	// Parse the output to find MJPEG and YUYV formats with their resolutions and fps
	// The output format is:
	//   [N]: 'MJPG' (Motion-JPEG, compressed)
	//           Size: Discrete 1920x1080
	//                   Interval: Discrete 0.033s (30.000 fps)
	//                   Interval: Discrete 0.042s (24.000 fps)
	//           Size: Discrete 1280x720
	//                   ...
	type formatInfo struct {
		pixFmt string
		width  int
		height int
		fps    int
	}

	var formats []formatInfo
	var currentPixFmt string
	var currentWidth, currentHeight int

	formatRe := regexp.MustCompile(`'(MJPG|YUYV|H264)'`)
	sizeRe := regexp.MustCompile(`Size:\s+Discrete\s+(\d+)x(\d+)`)
	fpsRe := regexp.MustCompile(`\((\d+)\.000\s+fps\)`)

	// First pass: collect all lines
	var lines []string
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	// Second pass: parse with proper state tracking
	for _, line := range lines {
		// Check for format header
		if m := formatRe.FindStringSubmatch(line); m != nil {
			switch m[1] {
			case "MJPG":
				currentPixFmt = "mjpeg"
			case "YUYV":
				currentPixFmt = "yuyv422"
			case "H264":
				currentPixFmt = "h264"
			}
			currentWidth = 0
			currentHeight = 0
			continue
		}

		// Check for size line
		if m := sizeRe.FindStringSubmatch(line); m != nil {
			currentWidth = atoi(m[1])
			currentHeight = atoi(m[2])
			continue
		}

		// Check for fps line
		if m := fpsRe.FindStringSubmatch(line); m != nil && currentPixFmt != "" && currentWidth > 0 {
			fps := atoi(m[1])
			// Only record the highest fps for each format+size combination
			found := false
			for i := range formats {
				if formats[i].pixFmt == currentPixFmt && formats[i].width == currentWidth && formats[i].height == currentHeight {
					if fps > formats[i].fps {
						formats[i].fps = fps
					}
					found = true
					break
				}
			}
			if !found {
				formats = append(formats, formatInfo{pixFmt: currentPixFmt, width: currentWidth, height: currentHeight, fps: fps})
			}
		}
	}

	logging.InfoLogger.Printf("Device %s: found %d format/size/fps combinations", device, len(formats))
	for _, f := range formats {
		logging.InfoLogger.Printf("  %s %dx%d @ %d fps", f.pixFmt, f.width, f.height, f.fps)
	}

	if len(formats) == 0 {
		return nil
	}

	// For replays: cap at 1080p, require decent fps, prefer MJPEG
	const maxWidth = 1920
	const maxHeight = 1080
	const minFps = 24

	// Filter: only formats at or below 1080p with acceptable fps
	var candidates []formatInfo
	for _, f := range formats {
		if f.width <= maxWidth && f.height <= maxHeight && f.fps >= minFps {
			candidates = append(candidates, f)
		}
	}
	// If no candidates meet the criteria, relax fps requirement but keep resolution cap
	if len(candidates) == 0 {
		for _, f := range formats {
			if f.width <= maxWidth && f.height <= maxHeight {
				candidates = append(candidates, f)
			}
		}
	}
	// If still nothing (all formats exceed 1080p), use all formats
	if len(candidates) == 0 {
		candidates = formats
	}

	// Selection priority: MJPEG > others, then highest fps, then highest resolution
	var best *formatInfo
	for i := range candidates {
		f := &candidates[i]
		if best == nil {
			best = f
			continue
		}
		// Prefer MJPEG over other formats
		if f.pixFmt == "mjpeg" && best.pixFmt != "mjpeg" {
			best = f
		} else if f.pixFmt == best.pixFmt {
			// Same format: prefer higher fps first, then higher resolution
			if f.fps > best.fps {
				best = f
			} else if f.fps == best.fps && f.width*f.height > best.width*best.height {
				best = f
			}
		}
	}

	return &detectedCamera{
		name:   name,
		device: device,
		format: "v4l2",
		pixFmt: best.pixFmt,
		size:   fmt.Sprintf("%dx%d", best.width, best.height),
		fps:    best.fps,
	}
}

// detectCamerasWindows uses ffmpeg dshow to detect cameras
func detectCamerasWindows() []detectedCamera {
	path := config.GetFFmpegPath()
	if path == "" {
		path = "ffmpeg"
	}

	// List devices
	cmd := exec.Command(path, "-hide_banner", "-f", "dshow", "-list_devices", "true", "-i", "dummy")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.Run() // This always returns error because "dummy" isn't a real device

	var cameraNames []string
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "(video)") {
			start := strings.Index(line, "\"")
			end := strings.LastIndex(line, "\"")
			if start != -1 && end != -1 && start != end {
				cameraNames = append(cameraNames, line[start+1:end])
			}
		}
	}

	var cameras []detectedCamera
	for _, name := range cameraNames {
		cam := probeDshowDevice(path, name)
		if cam != nil {
			cameras = append(cameras, *cam)
		}
	}
	return cameras
}

// probeDshowDevice probes a single dshow device for its capabilities
func probeDshowDevice(ffmpegPath, name string) *detectedCamera {
	cmd := exec.Command(ffmpegPath, "-hide_banner", "-f", "dshow", "-list_options", "true", "-i", fmt.Sprintf("video=%s", name))
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.Run() // Always returns error

	// Parse output for pixel formats, sizes, fps
	// Lines like: "  pixel_format=mjpeg  min s=1920x1080 fps=30 ..."
	//         or: "  vcodec=mjpeg  min s=1920x1080 fps=30 ..."
	type optionInfo struct {
		pixFmt string
		width  int
		height int
		fps    int
	}

	var options []optionInfo
	sizeRe := regexp.MustCompile(`s=(\d+)x(\d+)`)
	fpsRe := regexp.MustCompile(`fps=(\d+)`)

	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		line := scanner.Text()
		// Determine pixel format from the line
		var pixFmt string
		if strings.Contains(line, "pixel_format=mjpeg") || strings.Contains(line, "vcodec=mjpeg") {
			pixFmt = "mjpeg"
		} else if strings.Contains(line, "pixel_format=yuyv422") || strings.Contains(line, "pixel_format=yuyv") {
			pixFmt = "yuyv422"
		} else {
			continue
		}

		sizeMatch := sizeRe.FindStringSubmatch(line)
		fpsMatch := fpsRe.FindStringSubmatch(line)
		if sizeMatch == nil {
			continue
		}

		w := atoi(sizeMatch[1])
		h := atoi(sizeMatch[2])
		fps := 30
		if fpsMatch != nil {
			fps = atoi(fpsMatch[1])
		}

		options = append(options, optionInfo{pixFmt: pixFmt, width: w, height: h, fps: fps})
	}

	if len(options) == 0 {
		// Camera found but couldn't parse formats; add with defaults
		return &detectedCamera{
			name:   name,
			device: name,
			format: "dshow",
			pixFmt: "unknown",
			size:   "1280x720",
			fps:    30,
		}
	}

	// For replays: cap at 1080p, require decent fps, prefer MJPEG
	const maxW = 1920
	const maxH = 1080
	const minFps = 24

	// Filter: only options at or below 1080p with acceptable fps
	var candidates []optionInfo
	for _, o := range options {
		if o.width <= maxW && o.height <= maxH && o.fps >= minFps {
			candidates = append(candidates, o)
		}
	}
	if len(candidates) == 0 {
		for _, o := range options {
			if o.width <= maxW && o.height <= maxH {
				candidates = append(candidates, o)
			}
		}
	}
	if len(candidates) == 0 {
		candidates = options
	}

	// Selection priority: MJPEG > others, then highest fps, then highest resolution
	var best *optionInfo
	for i := range candidates {
		o := &candidates[i]
		if best == nil {
			best = o
			continue
		}
		if o.pixFmt == "mjpeg" && best.pixFmt != "mjpeg" {
			best = o
		} else if o.pixFmt == best.pixFmt {
			if o.fps > best.fps {
				best = o
			} else if o.fps == best.fps && o.width*o.height > best.width*best.height {
				best = o
			}
		}
	}

	return &detectedCamera{
		name:   name,
		device: name,
		format: "dshow",
		pixFmt: best.pixFmt,
		size:   fmt.Sprintf("%dx%d", best.width, best.height),
		fps:    best.fps,
	}
}

// writeAutoConfig generates auto.toml from detected hardware
func writeAutoConfig(outputPath string, cameras []detectedCamera, encoders []hwEncoder) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}

	var buf bytes.Buffer

	buf.WriteString("# Auto-detected camera configuration\n")
	buf.WriteString("# Generated by hardware auto-detection\n")
	buf.WriteString("# Review and copy desired sections to config.toml\n\n")

	// Pick the best hardware encoder (prefer GPU over software)
	bestEncoder := pickBestEncoder(encoders)

	for i, cam := range cameras {
		sectionName := fmt.Sprintf("camera%d", i+1)
		buf.WriteString(fmt.Sprintf("[%s]\n", sectionName))
		buf.WriteString(fmt.Sprintf("    description = \"%s (%s, %s)\"\n", cam.name, cam.pixFmt, cam.size))
		buf.WriteString(fmt.Sprintf("    enabled = true\n"))

		if cam.format == "dshow" {
			buf.WriteString(fmt.Sprintf("    camera = 'video=%s'\n", cam.device))
		} else {
			buf.WriteString(fmt.Sprintf("    camera = '%s'\n", cam.device))
		}

		buf.WriteString(fmt.Sprintf("    format = '%s'\n", cam.format))
		buf.WriteString(fmt.Sprintf("    size = \"%s\"\n", cam.size))
		buf.WriteString(fmt.Sprintf("    fps = %d\n", cam.fps))
		buf.WriteString("\n")

		if cam.pixFmt == "mjpeg" {
			// MJPEG: copy during recording, recode on trim
			if cam.format == "dshow" {
				buf.WriteString("    inputParameters = '-pixel_format mjpeg -rtbufsize 512M -thread_queue_size 4096'\n")
			} else {
				buf.WriteString("    inputParameters = '-input_format mjpeg -rtbufsize 512M -thread_queue_size 4096'\n")
			}
			buf.WriteString("    outputParameters = '-vcodec copy -an'\n")
			buf.WriteString("    recode = true\n")
		} else if cam.pixFmt == "h264" {
			// Camera outputs H.264 directly: copy during recording, no recode
			buf.WriteString("    inputParameters = '-rtbufsize 512M -thread_queue_size 4096'\n")
			buf.WriteString("    outputParameters = '-c:v copy -an'\n")
			buf.WriteString("    recode = false\n")
		} else {
			// YUV or unknown: must encode during recording
			if bestEncoder != nil {
				buf.WriteString(fmt.Sprintf("    inputParameters = '%s'\n", bestEncoder.inputParameters))
				buf.WriteString(fmt.Sprintf("    outputParameters = '%s'\n", bestEncoder.outputParameters))
			} else {
				// Software fallback
				inputParams := "-rtbufsize 512M -thread_queue_size 4096"
				if cam.format == "dshow" {
					inputParams = "-pixel_format yuyv422 " + inputParams
				} else {
					inputParams = "-input_format yuyv422 " + inputParams
				}
				buf.WriteString(fmt.Sprintf("    inputParameters = '%s'\n", inputParams))
				buf.WriteString(fmt.Sprintf("    outputParameters = '-c:v libx264 -preset ultrafast -crf 18 -pix_fmt yuv420p -an'\n"))
			}
			buf.WriteString("    recode = false\n")
		}
		buf.WriteString("\n")
	}

	// Append detected encoders as reference
	buf.WriteString("# =======================================================\n")
	buf.WriteString("# Detected H.264 encoders on this system\n")
	buf.WriteString("# =======================================================\n")
	if len(encoders) == 0 {
		buf.WriteString("# None found - only software encoding (libx264) is available\n")
	}
	for _, enc := range encoders {
		buf.WriteString(fmt.Sprintf("# %s - %s\n", enc.name, enc.description))
	}
	buf.WriteString("# libx264 - Software encoder (always available)\n")

	if err := os.WriteFile(outputPath, buf.Bytes(), 0644); err != nil {
		return err
	}

	logging.InfoLogger.Printf("Wrote auto-detected config to: %s", outputPath)
	return nil
}

// pickBestEncoder selects the best available hardware encoder
func pickBestEncoder(encoders []hwEncoder) *hwEncoder {
	// Preference order: nvenc > amf > vaapi > qsv
	preference := []string{"h264_nvenc", "h264_amf", "h264_vaapi", "h264_qsv"}
	for _, pref := range preference {
		for i := range encoders {
			if encoders[i].name == pref {
				return &encoders[i]
			}
		}
	}
	return nil
}

// buildSummary creates a human-readable summary of detected hardware
func buildSummary(cameras []detectedCamera, encoders []hwEncoder, outputPath string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Configuration written to:\n%s\n\n", outputPath))

	sb.WriteString("Cameras detected:\n")
	for i, cam := range cameras {
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, cam.name))
		sb.WriteString(fmt.Sprintf("     Device: %s\n", cam.device))
		sb.WriteString(fmt.Sprintf("     Format: %s  Size: %s  FPS: %d\n", cam.pixFmt, cam.size, cam.fps))
	}

	sb.WriteString("\nH.264 encoders available:\n")
	if len(encoders) == 0 {
		sb.WriteString("  (none - software encoding only)\n")
	}
	for _, enc := range encoders {
		sb.WriteString(fmt.Sprintf("  - %s (%s)\n", enc.name, enc.description))
	}
	sb.WriteString("  - libx264 (software, always available)\n")

	sb.WriteString("\nReview auto.toml and copy the sections you want into config.toml")
	return sb.String()
}

// showAutoDetectResults shows the detection summary in a dialog
func showAutoDetectResults(summary, outputPath string, window fyne.Window) {
	textArea := widget.NewMultiLineEntry()
	textArea.SetText(summary)
	textArea.Wrapping = fyne.TextWrapWord
	textArea.SetMinRowsVisible(12)

	scrollable := container.NewScroll(textArea)
	scrollable.SetMinSize(fyne.NewSize(500, 300))

	d := dialog.NewCustom("Auto-Detect Results", "Close", scrollable, window)
	d.Resize(fyne.NewSize(550, 400))
	d.Show()
}

// atoi is a simple string-to-int helper that returns 0 on error
func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
