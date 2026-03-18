package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/owlcms/replays/internal/config"
	camerascfg "github.com/owlcms/replays/internal/config/cameras"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/recording"
)

type sourceSpec struct {
	Key        string
	SourceType string
	Name       string
	ShortID    string
	Summary    string
	Enabled    bool
	OutputPort int
	Transport  string
	Camera     recording.DetectedCamera
	RTSP       camerascfg.RTSPSource
}

type sourceInventory struct {
	USB    []sourceSpec
	RTSP   []sourceSpec
	Active []sourceSpec
	Status string
	Errors []string
	Encoder *recording.HwEncoder
}

func currentStartPort() int {
	if camerasConfig != nil && camerasConfig.Unicast.Enabled {
		return camerasConfig.Unicast.StartPort
	}
	if camerasConfig != nil {
		return camerasConfig.Multicast.StartPort
	}
	return 9001
}

func buildSourceInventory() sourceInventory {
	startPort := currentStartPort()
	usedPorts := make(map[int]struct{})
	usedShortIDs := make(map[string]struct{})

	rtspSpecs := buildRTSPSources(startPort, usedPorts, usedShortIDs)
	usbSpecs := buildUSBSources(startPort, usedPorts, usedShortIDs)

	active := make([]sourceSpec, 0, len(rtspSpecs)+len(usbSpecs))
	for _, src := range usbSpecs {
		active = append(active, src)
	}
	for _, src := range rtspSpecs {
		if src.Enabled && strings.TrimSpace(src.RTSP.RTSPURL) != "" {
			active = append(active, src)
		}
	}

	sort.Slice(active, func(i, j int) bool {
		if active[i].OutputPort != active[j].OutputPort {
			return active[i].OutputPort < active[j].OutputPort
		}
		return active[i].Name < active[j].Name
	})

	inv := sourceInventory{
		USB:    usbSpecs,
		RTSP:   rtspSpecs,
		Active: active,
	}

	if conflicts := detectPortConflicts(active); len(conflicts) > 0 {
		inv.Errors = append(inv.Errors, conflicts...)
		inv.Status = "Port conflicts detected. Resolve source output ports before streaming."
		inv.Active = nil
		return inv
	}

	encoders := recording.DetectEncodersWithConfig(ffmpegConfig)
	inv.Encoder = recording.PickBestEncoder(encoders)

	switch {
	case len(inv.Active) == 0 && len(inv.RTSP) > 0:
		inv.Status = "No active sources. Enable an RTSP source or connect a camera."
	case len(inv.Active) == 0:
		inv.Status = "No cameras detected. Connect a camera or add an RTSP source."
	default:
		inv.Status = fmt.Sprintf("%d source(s) ready.", len(inv.Active))
	}

	return inv
}

func buildUSBSources(startPort int, usedPorts map[int]struct{}, usedShortIDs map[string]struct{}) []sourceSpec {
	cameras := recording.DetectCameras()
	assignments := make(map[string]camerascfg.DeviceAssignment, len(camerasConfig.DeviceAssignments))
	for _, assignment := range camerasConfig.DeviceAssignments {
		if strings.TrimSpace(assignment.MatchKey) == "" {
			continue
		}
		assignments[assignment.MatchKey] = assignment
		if assignment.OutputPort > 0 {
			usedPorts[assignment.OutputPort] = struct{}{}
		}
		if strings.TrimSpace(assignment.ShortID) != "" {
			usedShortIDs[strings.ToUpper(strings.TrimSpace(assignment.ShortID))] = struct{}{}
		}
	}

	var filtered []recording.DetectedCamera
	for _, cam := range cameras {
		if !camerasConfig.Cameras.IncludeAll && isIntegratedCamera(cam) {
			continue
		}
		filtered = append(filtered, cam)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return ffmpegConfig.FormatPriorityValue(filtered[i].PixFmt) > ffmpegConfig.FormatPriorityValue(filtered[j].PixFmt)
	})

	sources := make([]sourceSpec, 0, len(filtered))
	for _, cam := range filtered {
		assignment := assignments[cam.MatchKey]
		name := strings.TrimSpace(assignment.Name)
		if name == "" {
			name = cam.Name
		}
		shortID := strings.TrimSpace(assignment.ShortID)
		if shortID == "" {
			shortID = nextShortID("C", usedShortIDs)
		} else {
			usedShortIDs[strings.ToUpper(shortID)] = struct{}{}
		}
		port := assignment.OutputPort
		if port <= 0 {
			port = nextAvailablePort(startPort, usedPorts)
		}
		usedPorts[port] = struct{}{}

		cam.Name = name
		sources = append(sources, sourceSpec{
			Key:        cam.MatchKey,
			SourceType: "usb",
			Name:       name,
			ShortID:    shortID,
			Summary:    summarizeUSBIdentity(cam),
			Enabled:    true,
			OutputPort: port,
			Camera:     cam,
		})
	}

	return sources
}

func buildRTSPSources(startPort int, usedPorts map[int]struct{}, usedShortIDs map[string]struct{}) []sourceSpec {
	for _, assignment := range camerasConfig.DeviceAssignments {
		if assignment.OutputPort > 0 {
			usedPorts[assignment.OutputPort] = struct{}{}
		}
		if strings.TrimSpace(assignment.ShortID) != "" {
			usedShortIDs[strings.ToUpper(strings.TrimSpace(assignment.ShortID))] = struct{}{}
		}
	}

	sources := make([]sourceSpec, 0, len(camerasConfig.RTSPSources))
	for i := range camerasConfig.RTSPSources {
		src := &camerasConfig.RTSPSources[i]
		if strings.TrimSpace(src.ShortID) != "" {
			usedShortIDs[strings.ToUpper(strings.TrimSpace(src.ShortID))] = struct{}{}
		}
	}
	for i := range camerasConfig.RTSPSources {
		src := &camerasConfig.RTSPSources[i]
		if strings.TrimSpace(src.ShortID) == "" {
			src.ShortID = nextShortID("R", usedShortIDs)
		}
		port := src.OutputPort
		if port <= 0 {
			port = nextAvailablePort(startPort, usedPorts)
			src.OutputPort = port
		}
		usedPorts[port] = struct{}{}

		camera := rtspSourceCamera(*src)
		camera.Name = strings.TrimSpace(src.Name)
		if camera.Name == "" {
			camera.Name = fmt.Sprintf("RTSP %d", i+1)
		}
		camera.MatchKey = src.SourceID
		camera.Identity = summarizeRTSPURL(src.RTSPURL)

		sources = append(sources, sourceSpec{
			Key:        src.SourceID,
			SourceType: "rtsp",
			Name:       camera.Name,
			ShortID:    src.ShortID,
			Summary:    summarizeRTSPURL(src.RTSPURL),
			Enabled:    src.Enabled,
			OutputPort: port,
			Transport:  src.Transport,
			Camera:     camera,
			RTSP:       *src,
		})
	}

	return sources
}

func nextAvailablePort(startPort int, used map[int]struct{}) int {
	port := startPort
	if port <= 0 {
		port = 9001
	}
	for {
		if _, exists := used[port]; !exists {
			return port
		}
		port++
	}
}

func nextShortID(prefix string, used map[string]struct{}) string {
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s%d", prefix, i)
		key := strings.ToUpper(candidate)
		if _, exists := used[key]; exists {
			continue
		}
		used[key] = struct{}{}
		return candidate
	}
}

func detectPortConflicts(sources []sourceSpec) []string {
	byPort := make(map[int][]string)
	for _, src := range sources {
		if src.OutputPort <= 0 {
			continue
		}
		byPort[src.OutputPort] = append(byPort[src.OutputPort], src.Name)
	}
	var conflicts []string
	for port, names := range byPort {
		if len(names) < 2 {
			continue
		}
		conflicts = append(conflicts, fmt.Sprintf("Port %d is assigned to %s", port, strings.Join(names, ", ")))
	}
	sort.Strings(conflicts)
	return conflicts
}

func summarizeUSBIdentity(cam recording.DetectedCamera) string {
	if strings.TrimSpace(cam.Identity) != "" {
		return cam.Identity
	}
	return cam.Device
}

func summarizeRTSPURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	parsed.User = nil
	path := strings.TrimSpace(parsed.Path)
	if path == "" {
		path = "/"
	}
	host := parsed.Host
	if host == "" {
		host = parsed.Opaque
	}
	return host + path
}

// normalizeRTSPURL ensures the URL has at least a "/" path so that servers
// which require it (e.g. many phone RTSP apps) are reached correctly.
func normalizeRTSPURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Path != "" {
		return raw
	}
	parsed.Path = "/"
	return parsed.String()
}

// probeUSBSource re-detects all cameras and finds the one with the given MatchKey.
// Runs the detection in a goroutine and calls onDone on the UI goroutine.
// onDone receives nil if the camera is not found or not connected.
func probeUSBSource(matchKey string, onDone func(cam *recording.DetectedCamera)) {
	go func() {
		cameras := recording.DetectCamerasWithConfig(ffmpegConfig)
		for _, cam := range cameras {
			if cam.MatchKey == matchKey {
				c := cam
				onDone(&c)
				return
			}
		}
		onDone(nil)
	}()
}

// rtspSourceCamera builds a DetectedCamera for an RTSP source without probing.
// Uses the stored codec if probed previously; defaults to h264 otherwise.
func rtspSourceCamera(src camerascfg.RTSPSource) recording.DetectedCamera {
	codec := strings.ToLower(strings.TrimSpace(src.Codec))
	if codec == "" {
		codec = "h264"
	}
	return recording.DetectedCamera{
		Name:     strings.TrimSpace(src.Name),
		Device:   normalizeRTSPURL(src.RTSPURL),
		Format:   "rtsp",
		PixFmt:   codec,
		Size:     "-",
		Fps:      0,
		MatchKey: src.SourceID,
		Identity: summarizeRTSPURL(src.RTSPURL),
	}
}

// probeAndFillRTSPRow runs ffprobe on the row's URL in the background,
// updates detectedCodec, and calls onDone(codec, size, fps, err).
func probeAndFillRTSPRow(src camerascfg.RTSPSource, onDone func(codec, size string, fps int, err error)) {
	go func() {
		detected := probeRTSPSource(src)
		codec := strings.ToLower(strings.TrimSpace(detected.PixFmt))
		if codec == "unknown" || codec == "" {
			onDone("", "-", 0, fmt.Errorf("probe failed or no video stream found"))
			return
		}
		onDone(codec, detected.Size, detected.Fps, nil)
	}()
}

// probeRTSPSource connects to the RTSP URL with ffprobe to detect stream parameters.
// Not used by default — see rtspSourceCamera. Retained for potential future use.
func probeRTSPSource(src camerascfg.RTSPSource) recording.DetectedCamera {
	normalizedURL := normalizeRTSPURL(src.RTSPURL)
	cam := recording.DetectedCamera{
		Name:     strings.TrimSpace(src.Name),
		Device:   normalizedURL,
		Format:   "rtsp",
		PixFmt:   "unknown",
		Size:     "-",
		Fps:      0,
		MatchKey: src.SourceID,
		Identity: summarizeRTSPURL(src.RTSPURL),
	}
	if !src.Enabled || strings.TrimSpace(src.RTSPURL) == "" {
		return cam
	}

	ffprobePath := resolveFFprobePathForRTSP()
	args := []string{"-v", "error"}
	transport := strings.ToLower(strings.TrimSpace(src.Transport))
	if transport == "tcp" || transport == "udp" {
		args = append(args, "-rtsp_transport", transport)
	}
	args = append(args,
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height,avg_frame_rate",
		"-of", "default=noprint_wrappers=1:nokey=0",
		normalizedURL,
	)

	cmd := recording.CreateHiddenCmd(ffprobePath, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		logging.WarningLogger.Printf("ffprobe RTSP probe failed for %s: %v", summarizeRTSPURL(src.RTSPURL), err)
		return cam
	}

	values := make(map[string]string)
	for _, line := range strings.Split(out.String(), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) == 2 {
			values[parts[0]] = parts[1]
		}
	}

	codec := strings.ToLower(strings.TrimSpace(values["codec_name"]))
	if codec != "" {
		cam.PixFmt = codec
	}
	width, _ := strconv.Atoi(strings.TrimSpace(values["width"]))
	height, _ := strconv.Atoi(strings.TrimSpace(values["height"]))
	if width > 0 && height > 0 {
		cam.Size = fmt.Sprintf("%dx%d", width, height)
	}
	cam.Fps = parseProbeFPS(values["avg_frame_rate"])
	return cam
}

func parseProbeFPS(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0/0" {
		return 0
	}
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0
		}
		return int(value + 0.5)
	}
	numerator, errNum := strconv.ParseFloat(parts[0], 64)
	denominator, errDen := strconv.ParseFloat(parts[1], 64)
	if errNum != nil || errDen != nil || denominator == 0 {
		return 0
	}
	return int((numerator / denominator) + 0.5)
}

func resolveFFprobePathForRTSP() string {
	if envPath := strings.TrimSpace(config.GetFFmpegPath()); envPath != "" {
		name := "ffprobe"
		if runtime.GOOS == "windows" {
			name = "ffprobe.exe"
		}
		candidate := filepath.Join(filepath.Dir(envPath), name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	name := "ffprobe"
	if runtime.GOOS == "windows" {
		name = "ffprobe.exe"
	}
	if shared := config.FindSharedFFmpegExecutable(name); shared != "" {
		return shared
	}
	return name
}