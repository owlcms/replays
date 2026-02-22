package recording

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
)

// DetectedCamera holds information about a detected camera device
type DetectedCamera struct {
	Name   string
	Device string // device path (Linux) or device name (Windows)
	Format string // v4l2 or dshow
	PixFmt string // mjpeg, yuyv422, etc.
	Size   string // best resolution found
	Fps    int    // best fps for that resolution
}

// HwEncoder holds information about a detected hardware encoder
type HwEncoder struct {
	Name             string // h264_nvenc, h264_vaapi, h264_amf, h264_qsv
	Description      string
	InputParameters  string
	OutputParameters string
	TestInit         string // extra flags needed for encoder test (e.g. VAAPI/QSV init)
}

type cameraMode struct {
	pixFmt string
	width  int
	height int
	fps    int
}

// DetectAndWriteConfig probes cameras and GPU encoders, then writes auto.toml.
// It loads cameras_config.toml configuration so auto.toml benefits from the same
// intelligent encoder definitions, format priorities, and mode priorities
// used by the cameras program.
func DetectAndWriteConfig(window fyne.Window) {
	logging.InfoLogger.Println("Starting hardware auto-detection...")

	progressLabel := widget.NewLabel("Detecting hardware encoders...")
	progressDialog := dialog.NewCustomWithoutButtons("Auto-Detecting Hardware", progressLabel, window)
	progressDialog.Show()

	go func() {
		defer progressDialog.Hide()

		// Load shared cameras config so we use config-driven encoder & priority logic
		cameraCfg, cfgErr := config.LoadCamerasConfig()
		if cfgErr != nil {
			logging.WarningLogger.Printf("Could not load cameras shared config, using legacy defaults: %v", cfgErr)
		}

		// Step 1: Detect available H.264 hardware encoders (config-driven when available)
		progressLabel.SetText("Detecting hardware encoders...")
		encoders := DetectEncodersWithConfig(cameraCfg)
		logging.InfoLogger.Printf("Detected %d hardware encoders", len(encoders))

		// Step 2: Detect cameras (using config-driven mode priorities)
		progressLabel.SetText("Detecting cameras...")
		cameras := DetectCamerasWithConfig(cameraCfg)
		logging.InfoLogger.Printf("Detected %d cameras", len(cameras))

		// Step 3: Write auto.toml (even with 0 cameras, to show detected encoders)
		progressLabel.SetText("Writing auto.toml...")
		// Output to autoTomlDir if set, otherwise install dir
		var outputPath string
		if config.AutoTomlDir != "" {
			outputPath = filepath.Join(config.AutoTomlDir, "auto.toml")
		} else {
			outputPath = filepath.Join(config.GetInstallDir(), "auto.toml")
		}
		err := writeAutoConfig(outputPath, cameras, encoders, cameraCfg)
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

// DetectEncoders probes ffmpeg for available H.264 hardware encoders.
// If a CamerasConfig is provided, encoder definitions come from the config.
// If cfg is nil, falls back to the legacy hardcoded definitions.
func DetectEncoders() []HwEncoder {
	return DetectEncodersWithConfig(nil)
}

// DetectEncodersWithConfig probes ffmpeg for available H.264 hardware encoders,
// using the encoder definitions from cfg.
func DetectEncodersWithConfig(cfg *config.CamerasConfig) []HwEncoder {
	path := config.GetFFmpegPath()
	if path == "" {
		path = "ffmpeg"
	}

	cmd := CreateHiddenCmd(path, "-hide_banner", "-encoders")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		logging.ErrorLogger.Printf("Failed to query ffmpeg encoders: %v", err)
		return nil
	}

	// Collect all h264_* encoder names that ffmpeg reports
	availableEncoders := make(map[string]bool)
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "h264_") {
			continue
		}
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		availableEncoders[fields[1]] = true
	}

	// Build candidate list from config definitions (order = preference)
	var candidates []HwEncoder
	if cfg != nil && len(cfg.Encoders) > 0 {
		for _, enc := range cfg.Encoders {
			if availableEncoders[enc.Name] {
				candidates = append(candidates, HwEncoder{
					Name:             enc.Name,
					Description:      enc.Description,
					InputParameters:  enc.InputParameters,
					OutputParameters: enc.OutputParameters,
					TestInit:         enc.TestInit,
				})
			}
		}
	} else {
		// Legacy hardcoded fallback (used when cfg is nil, e.g. from replays)
		candidates = legacyEncoderCandidates(availableEncoders)
	}

	// Verify each candidate encoder actually works on this hardware
	var found []HwEncoder
	for _, enc := range candidates {
		if testEncoderWithInit(path, enc) {
			logging.InfoLogger.Printf("Encoder %s verified working", enc.Name)
			found = append(found, enc)
		} else {
			logging.InfoLogger.Printf("Encoder %s compiled in but not functional on this hardware", enc.Name)
		}
	}
	return found
}

// legacyEncoderCandidates returns the hardcoded encoder definitions used
// when no CamerasConfig is available (backward-compatible with replays).
func legacyEncoderCandidates(available map[string]bool) []HwEncoder {
	var candidates []HwEncoder
	if available["h264_nvenc"] {
		candidates = append(candidates, HwEncoder{
			Name: "h264_nvenc", Description: "NVIDIA GPU",
			InputParameters:  "-rtbufsize 512M -thread_queue_size 4096",
			OutputParameters: "-c:v h264_nvenc -preset p5 -rc cbr -b:v 8M",
		})
	}
	if available["h264_amf"] {
		candidates = append(candidates, HwEncoder{
			Name: "h264_amf", Description: "AMD GPU (AMF)",
			InputParameters:  "-rtbufsize 512M -thread_queue_size 4096",
			OutputParameters: "-c:v h264_amf -rc cbr -b:v 8M",
		})
	}
	if available["h264_vaapi"] && runtime.GOOS == "linux" {
		candidates = append(candidates, HwEncoder{
			Name: "h264_vaapi", Description: "VAAPI (AMD/Intel on Linux)",
			InputParameters:  "-hwaccel vaapi -vaapi_device /dev/dri/renderD128 -rtbufsize 512M -thread_queue_size 4096",
			OutputParameters: "-c:v h264_vaapi -rc_mode CBR -b:v 8M",
			TestInit:         "-init_hw_device vaapi=va:/dev/dri/renderD128",
		})
	}
	if available["h264_qsv"] {
		candidates = append(candidates, HwEncoder{
			Name: "h264_qsv", Description: "Intel GPU (QSV)",
			InputParameters:  "-init_hw_device qsv=hw -filter_hw_device hw -rtbufsize 512M -thread_queue_size 4096",
			OutputParameters: "-c:v h264_qsv -preset medium -look_ahead 0 -rc_mode CBR -b:v 8M",
			TestInit:         "-init_hw_device qsv=hw -filter_hw_device hw",
		})
	}
	return candidates
}

// testEncoderWithInit tests whether an encoder actually works on this hardware,
// using the TestInit field for any required hardware init flags.
func testEncoderWithInit(ffmpegPath string, enc HwEncoder) bool {
	var args []string

	if enc.TestInit != "" {
		args = append(args, "-hide_banner", "-loglevel", "error")
		args = append(args, strings.Fields(enc.TestInit)...)
		args = append(args, "-f", "lavfi", "-i", "nullsrc=s=64x64:d=0.1")
		// VAAPI needs hwupload filter
		if strings.Contains(enc.Name, "vaapi") {
			args = append(args, "-vf", "format=nv12,hwupload")
		}
		args = append(args, "-c:v", enc.Name, "-f", "null", "-")
	} else {
		args = []string{"-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "nullsrc=s=64x64:d=0.1",
			"-c:v", enc.Name, "-f", "null", "-"}
	}

	cmd := CreateHiddenCmd(ffmpegPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		logging.InfoLogger.Printf("Encoder test for %s failed: %v (%s)", enc.Name, err, strings.TrimSpace(stderr.String()))
		return false
	}
	return true
}

// DetectCameras detects available camera devices and their capabilities.
// It uses legacy hardcoded priorities for mode selection.
func DetectCameras() []DetectedCamera {
	return DetectCamerasWithConfig(nil)
}

// DetectCamerasWithConfig detects cameras using config-driven mode priorities
// when cfg is non-nil; falls back to legacy hardcoded priorities otherwise.
func DetectCamerasWithConfig(cfg *config.CamerasConfig) []DetectedCamera {
	switch runtime.GOOS {
	case "linux":
		return detectCamerasLinux(cfg)
	case "windows":
		return detectCamerasWindows(cfg)
	default:
		return nil
	}
}

// detectCamerasLinux uses v4l2-ctl to detect cameras and their formats
func detectCamerasLinux(cfg *config.CamerasConfig) []DetectedCamera {
	// First get the device list
	cmd := CreateHiddenCmd("v4l2-ctl", "--list-devices")
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

	var cameras []DetectedCamera
	for _, dev := range devices {
		cam := probeV4L2Device(dev.name, dev.device, cfg)
		if cam != nil {
			cameras = append(cameras, *cam)
		}
	}
	return cameras
}

// probeV4L2Device probes a single v4l2 device for its best format
func probeV4L2Device(name, device string, cfg *config.CamerasConfig) *DetectedCamera {
	cmd := CreateHiddenCmd("v4l2-ctl", "-d", device, "--list-formats-ext")
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

	// Match common V4L2 pixel formats
	formatRe := regexp.MustCompile(`'(MJPG|YUYV|H264|NV12|RGB3|BGR3|UYVY)'`)
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
			// Compressed formats
			case "MJPG":
				currentPixFmt = "mjpeg"
			case "H264":
				currentPixFmt = "h264"
			// Raw formats - no decode needed
			case "YUYV":
				currentPixFmt = "yuyv422"
			case "NV12":
				currentPixFmt = "nv12"
			case "RGB3":
				currentPixFmt = "rgb24"
			case "BGR3":
				currentPixFmt = "bgr24"
			case "UYVY":
				currentPixFmt = "uyvy422"
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

	var modes []cameraMode
	for _, f := range formats {
		modes = append(modes, cameraMode{pixFmt: f.pixFmt, width: f.width, height: f.height, fps: f.fps})
	}

	best := PickBestCameraModeWithConfig(modes, cfg)

	return &DetectedCamera{
		Name:   name,
		Device: device,
		Format: "v4l2",
		PixFmt: best.pixFmt,
		Size:   fmt.Sprintf("%dx%d", best.width, best.height),
		Fps:    best.fps,
	}
}

// detectCamerasWindows uses ffmpeg dshow to detect cameras
func detectCamerasWindows(cfg *config.CamerasConfig) []DetectedCamera {
	path := config.GetFFmpegPath()
	if path == "" {
		path = "ffmpeg"
	}

	// List devices
	cmd := CreateHiddenCmd(path, "-hide_banner", "-f", "dshow", "-list_devices", "true", "-i", "dummy")
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

	var cameras []DetectedCamera
	for _, name := range cameraNames {
		cam := probeDshowDevice(path, name, cfg)
		if cam != nil {
			cameras = append(cameras, *cam)
		}
	}
	return cameras
}

// probeDshowDevice probes a single dshow device for its capabilities
func probeDshowDevice(ffmpegPath, name string, cfg *config.CamerasConfig) *DetectedCamera {
	cmd := CreateHiddenCmd(ffmpegPath, "-hide_banner", "-f", "dshow", "-list_options", "true", "-i", fmt.Sprintf("video=%s", name))
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
	// dshow -list_options lines have the form:
	//   vcodec=h264  min s=1920x1080 fps=15 max s=1920x1080 fps=60.0002
	// We want the MAX size and fps from each line.
	maxSizeFpsRe := regexp.MustCompile(`max\s+s=(\d+)x(\d+)\s+fps=([0-9.]+)`)
	// Fallback for lines without min/max (just a single s=... fps=...)
	fallbackSizeFpsRe := regexp.MustCompile(`s=(\d+)x(\d+)\s+fps=([0-9.]+)`)

	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		line := scanner.Text()
		// Determine pixel format from the line
		var pixFmt string
		// Compressed formats
		if strings.Contains(line, "pixel_format=mjpeg") || strings.Contains(line, "vcodec=mjpeg") {
			pixFmt = "mjpeg"
		} else if strings.Contains(line, "vcodec=h264") {
			pixFmt = "h264"
			// Raw formats - no decode needed, just encode
		} else if strings.Contains(line, "pixel_format=yuyv422") || strings.Contains(line, "pixel_format=yuyv") {
			pixFmt = "yuyv422"
		} else if strings.Contains(line, "pixel_format=nv12") {
			pixFmt = "nv12"
		} else if strings.Contains(line, "pixel_format=rgb24") {
			pixFmt = "rgb24"
		} else if strings.Contains(line, "pixel_format=bgr24") {
			pixFmt = "bgr24"
		} else if strings.Contains(line, "pixel_format=uyvy422") {
			pixFmt = "uyvy422"
		} else if strings.Contains(line, "pixel_format=") {
			// Catch other raw formats generically
			re := regexp.MustCompile(`pixel_format=(\w+)`)
			if m := re.FindStringSubmatch(line); m != nil {
				pixFmt = m[1]
			}
		} else {
			continue
		}

		// Try to match the "max" portion first; fall back to first s=...fps=...
		m := maxSizeFpsRe.FindStringSubmatch(line)
		if m == nil {
			m = fallbackSizeFpsRe.FindStringSubmatch(line)
		}
		if m == nil {
			continue
		}

		w := atoi(m[1])
		h := atoi(m[2])
		fps := parseFps(m[3])
		options = append(options, optionInfo{pixFmt: pixFmt, width: w, height: h, fps: fps})
	}

	if len(options) == 0 {
		// Camera found but couldn't parse formats; add with defaults
		return &DetectedCamera{
			Name:   name,
			Device: name,
			Format: "dshow",
			PixFmt: "unknown",
			Size:   "1280x720",
			Fps:    30,
		}
	}

	var modes []cameraMode
	for _, o := range options {
		modes = append(modes, cameraMode{pixFmt: o.pixFmt, width: o.width, height: o.height, fps: o.fps})
	}

	best := PickBestCameraModeWithConfig(modes, cfg)

	return &DetectedCamera{
		Name:   name,
		Device: name,
		Format: "dshow",
		PixFmt: best.pixFmt,
		Size:   fmt.Sprintf("%dx%d", best.width, best.height),
		Fps:    best.fps,
	}
}

// PickBestCameraModeWithConfig selects the best camera mode using config-driven priorities.
// If cfg is nil, uses legacy hardcoded priorities.
func PickBestCameraModeWithConfig(allModes []cameraMode, cfg *config.CamerasConfig) cameraMode {
	if len(allModes) == 0 {
		return cameraMode{pixFmt: "unknown", width: 1280, height: 720, fps: 30}
	}

	const maxWidth = 1920
	const maxHeight = 1080

	var candidates []cameraMode
	for _, mode := range allModes {
		if mode.width <= maxWidth && mode.height <= maxHeight {
			candidates = append(candidates, mode)
		}
	}
	if len(candidates) == 0 {
		candidates = allModes
	}

	if config.GetMjpeg720pOnly() {
		filtered := filterMjpegToMax720p(candidates)
		if len(filtered) > 0 {
			candidates = filtered
		}
	}

	best := candidates[0]
	for _, mode := range candidates[1:] {
		if modePreferredOverWithConfig(mode, best, cfg) {
			best = mode
		}
	}

	return best
}

func filterMjpegToMax720p(modes []cameraMode) []cameraMode {
	var filtered []cameraMode
	for _, mode := range modes {
		if mode.pixFmt == "mjpeg" && (mode.width > 1280 || mode.height > 720) {
			continue
		}
		filtered = append(filtered, mode)
	}
	return filtered
}

func modePreferredOverWithConfig(candidate, current cameraMode, cfg *config.CamerasConfig) bool {
	var candidateFormat, currentFormat int
	var candidateProfile, currentProfile int

	if cfg != nil {
		candidateFormat = cfg.FormatPriorityValue(candidate.pixFmt)
		currentFormat = cfg.FormatPriorityValue(current.pixFmt)
		candidateProfile = cfg.ProfilePriorityValue(candidate.width, candidate.height, candidate.fps)
		currentProfile = cfg.ProfilePriorityValue(current.width, current.height, current.fps)
	} else {
		candidateFormat = modeFormatPriority(candidate.pixFmt)
		currentFormat = modeFormatPriority(current.pixFmt)
		candidateProfile = modeProfilePriority(candidate)
		currentProfile = modeProfilePriority(current)
	}

	if candidateFormat != currentFormat {
		return candidateFormat > currentFormat
	}

	if candidateProfile != currentProfile {
		return candidateProfile > currentProfile
	}

	if candidate.fps != current.fps {
		return candidate.fps > current.fps
	}

	candidatePixels := candidate.width * candidate.height
	currentPixels := current.width * current.height
	return candidatePixels > currentPixels
}

func modeFormatPriority(pixFmt string) int {
	switch pixFmt {
	case "h264":
		return 3
	case "mjpeg":
		return 2
	default:
		return 1
	}
}

func modeProfilePriority(mode cameraMode) int {
	isFullHD := mode.width == 1920 && mode.height == 1080
	isHD := mode.width == 1280 && mode.height == 720

	switch {
	case isFullHD && mode.fps >= 59:
		return 4
	case isHD && mode.fps >= 59:
		return 3
	case isFullHD && mode.fps >= 29:
		return 2
	case isHD && mode.fps >= 29:
		return 1
	default:
		return 0
	}
}

// writeAutoConfig generates auto.toml from detected hardware.
// When cfg is non-nil, encoder output parameters and format logic come from
// the shared cameras configuration; otherwise legacy hardcoded values are used.
func writeAutoConfig(outputPath string, cameras []DetectedCamera, encoders []HwEncoder, cfg *config.CamerasConfig) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}

	// Backup existing auto.toml with versioned suffix (.1, .2, .3, ...)
	if _, err := os.Stat(outputPath); err == nil {
		for i := 1; ; i++ {
			backup := fmt.Sprintf("%s.%d", outputPath, i)
			if _, err := os.Stat(backup); os.IsNotExist(err) {
				if err := os.Rename(outputPath, backup); err != nil {
					logging.ErrorLogger.Printf("Failed to backup auto.toml to %s: %v", backup, err)
				} else {
					logging.InfoLogger.Printf("Backed up existing auto.toml to %s", backup)
				}
				break
			}
		}
	}

	var buf bytes.Buffer

	buf.WriteString("# Auto-detected camera configuration\n")
	buf.WriteString("# Generated by hardware auto-detection\n")
	buf.WriteString("# Review and copy desired sections to config.toml\n\n")
	buf.WriteString("# Camera source precedence in replays (highest to lowest):\n")
	buf.WriteString("# 1) multicast.toml mapping (when [multicast].enabled = true in config.toml)\n")
	buf.WriteString("# 2) auto.toml camera sections (this file, when multicast is disabled)\n")
	buf.WriteString("# 3) camera sections in config.toml ([windows*], [linux*], etc.)\n")
	buf.WriteString("#\n")
	buf.WriteString("# Note: this precedence applies only to camera source definitions.\n")
	buf.WriteString("# Other application settings still come from config.toml.\n\n")

	// Pick the best hardware encoder (prefer GPU over software)
	bestEncoder := PickBestEncoder(encoders)

	// Resolve the software fallback string from config or hardcoded default
	softwareFallback := "-c:v libx264 -preset ultrafast -crf 18 -pix_fmt yuv420p"
	if cfg != nil && cfg.Software.OutputParameters != "" {
		softwareFallback = cfg.Software.OutputParameters
	}

	for i, cam := range cameras {
		sectionName := fmt.Sprintf("camera%d", i+1)
		buf.WriteString(fmt.Sprintf("[%s]\n", sectionName))
		buf.WriteString(fmt.Sprintf("    description = \"%s (%s, %s)\"\n", cam.Name, cam.PixFmt, cam.Size))
		buf.WriteString("    enabled = true\n")

		if cam.Format == "dshow" {
			buf.WriteString(fmt.Sprintf("    camera = 'video=%s'\n", cam.Device))
		} else {
			buf.WriteString(fmt.Sprintf("    camera = '%s'\n", cam.Device))
		}

		buf.WriteString(fmt.Sprintf("    format = '%s'\n", cam.Format))
		buf.WriteString(fmt.Sprintf("    size = \"%s\"\n", cam.Size))
		buf.WriteString(fmt.Sprintf("    fps = %d\n", cam.Fps))
		buf.WriteString("\n")

		// Determine if format is compressed (needs decode) or raw (no decode needed)
		isCompressed := cam.PixFmt == "mjpeg" || cam.PixFmt == "h264"

		if isCompressed {
			// Compressed formats: copy during recording
			switch cam.PixFmt {
			case "mjpeg":
				// MJPEG: copy during recording, recode on trim
				if cam.Format == "dshow" {
					buf.WriteString("    inputParameters = '-pixel_format mjpeg -rtbufsize 512M -thread_queue_size 4096'\n")
				} else {
					buf.WriteString("    inputParameters = '-input_format mjpeg -rtbufsize 512M -thread_queue_size 4096'\n")
				}
				buf.WriteString("    outputParameters = '-vcodec copy -an'\n")
				buf.WriteString("    recode = true\n")
			case "h264":
				// H.264: copy during recording, no recode
				buf.WriteString("    inputParameters = '-rtbufsize 512M -thread_queue_size 4096'\n")
				buf.WriteString("    outputParameters = '-c:v copy -an'\n")
				buf.WriteString("    recode = false\n")
			}
		} else {
			// Raw formats (yuyv422, nv12, rgb24, etc.): no decode needed, just encode
			// Use camera's fps for GOP size and output frame rate
			fpsParams := fmt.Sprintf("-g %d -keyint_min %d -r %d -vsync cfr -an", cam.Fps, cam.Fps, cam.Fps)
			inputParams := "-rtbufsize 512M -thread_queue_size 4096"

			// Specify the pixel format for proper raw input handling
			if cam.Format == "dshow" {
				inputParams = fmt.Sprintf("-pixel_format %s %s", cam.PixFmt, inputParams)
			} else {
				inputParams = fmt.Sprintf("-input_format %s %s", cam.PixFmt, inputParams)
			}

			buf.WriteString(fmt.Sprintf("    inputParameters = '%s'\n", inputParams))

			if bestEncoder != nil {
				buf.WriteString(fmt.Sprintf("    outputParameters = '%s %s'\n", bestEncoder.OutputParameters, fpsParams))
			} else {
				// Software fallback (from shared cameras config or hardcoded default)
				buf.WriteString(fmt.Sprintf("    outputParameters = '%s %s'\n", softwareFallback, fpsParams))
			}
			buf.WriteString("    recode = false\n")
		}
		buf.WriteString("\n")
	}

	// If no local cameras were detected, add UDP stream templates
	if len(cameras) == 0 {
		buf.WriteString("# No local cameras detected.\n")
		buf.WriteString("# Sample UDP stream configurations for cameras attached to other machines on the network.\n\n")
		for i := 1; i <= 3; i++ {
			buf.WriteString(fmt.Sprintf("[camera%d]\n", i))
			buf.WriteString(fmt.Sprintf("    description = \"UDP Stream from Camera %d\"\n", i))
			buf.WriteString("    enabled = false\n")
			buf.WriteString(fmt.Sprintf("    camera = 'udp://239.255.0.1:900%d'\n", i))
			buf.WriteString("    format = 'mpegts'\n")
			buf.WriteString("    size = \"1920x1080\"\n")
			buf.WriteString("    fps = 60\n")
			buf.WriteString("\n")
			buf.WriteString("    # Input parameters: none needed - reading from multicast UDP stream\n")
			buf.WriteString("    inputParameters = ''\n")
			buf.WriteString("\n")
			buf.WriteString("    # Output parameters: copy H.264 stream directly (already encoded by sender)\n")
			buf.WriteString("    outputParameters = '-c:v copy -an'\n")
			buf.WriteString("\n")
			buf.WriteString("    # No recoding needed - stream is already H.264\n")
			buf.WriteString("    recode = false\n\n")
		}
	}

	// Append detected encoders as reference
	buf.WriteString("# =======================================================\n")
	buf.WriteString("# Detected H.264 encoders on this system\n")
	buf.WriteString("# =======================================================\n")
	if len(encoders) == 0 {
		buf.WriteString("# None found - only software encoding (libx264) is available\n")
	}
	for _, enc := range encoders {
		buf.WriteString(fmt.Sprintf("# %s - %s\n", enc.Name, enc.Description))
	}
	buf.WriteString("# libx264 - Software encoder (always available)\n")

	if err := os.WriteFile(outputPath, buf.Bytes(), 0644); err != nil {
		return err
	}

	logging.InfoLogger.Printf("Wrote auto-detected config to: %s", outputPath)
	return nil
}

// PickBestEncoder selects the best available hardware encoder.
// When DetectEncodersWithConfig was used, encoders are already in config
// preference order, so the first one wins. For the legacy path, we apply
// a hardcoded preference: nvenc > amf > vaapi > qsv.
func PickBestEncoder(encoders []HwEncoder) *HwEncoder {
	if len(encoders) > 0 {
		return &encoders[0]
	}
	return nil
}

// buildSummary creates a human-readable summary of detected hardware
func buildSummary(cameras []DetectedCamera, encoders []HwEncoder, outputPath string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Configuration written to:\n%s\n\n", outputPath))

	sb.WriteString("Cameras detected:\n")
	for i, cam := range cameras {
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, cam.Name))
		sb.WriteString(fmt.Sprintf("     Device: %s\n", cam.Device))
		sb.WriteString(fmt.Sprintf("     Format: %s  Size: %s  FPS: %d\n", cam.PixFmt, cam.Size, cam.Fps))
	}

	sb.WriteString("\nH.264 encoders available:\n")
	if len(encoders) == 0 {
		sb.WriteString("  (none - software encoding only)\n")
	}
	for _, enc := range encoders {
		sb.WriteString(fmt.Sprintf("  - %s (%s)\n", enc.Name, enc.Description))
	}
	sb.WriteString("  - libx264 (software, always available)\n")

	sb.WriteString("\nReview auto.toml and copy the sections you want into config.toml")
	return sb.String()
}

// showAutoDetectResults shows the detection summary in a dialog
func showAutoDetectResults(summary, _ string, window fyne.Window) {
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

// parseFps parses a possibly-decimal fps string (e.g. "60.0002", "29.97")
// and returns the nearest integer, rounding to handle NTSC-style fractional values.
func parseFps(s string) int {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return atoi(s)
	}
	return int(f + 0.5)
}
