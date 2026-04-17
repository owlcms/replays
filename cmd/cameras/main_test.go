package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	camerascfg "github.com/owlcms/replays/internal/config/cameras"
	"github.com/owlcms/replays/internal/recording"
)

func TestRTSPRetryDelay(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 2 * time.Second},
		{attempt: 1, want: 4 * time.Second},
		{attempt: 2, want: 8 * time.Second},
		{attempt: 3, want: 8 * time.Second},
	}

	for _, tc := range tests {
		if got := rtspRetryDelay(tc.attempt); got != tc.want {
			t.Fatalf("rtspRetryDelay(%d) = %s, want %s", tc.attempt, got, tc.want)
		}
	}
}

func TestRTSPRetryWindow(t *testing.T) {
	tests := []struct {
		name           string
		retriesStarted int
		wantDelay      time.Duration
		wantRetry      bool
	}{
		{name: "first retry uses 2 seconds", retriesStarted: 0, wantDelay: 2 * time.Second, wantRetry: true},
		{name: "second retry uses 4 seconds", retriesStarted: 1, wantDelay: 4 * time.Second, wantRetry: true},
		{name: "third retry uses 8 seconds", retriesStarted: 2, wantDelay: 8 * time.Second, wantRetry: true},
		{name: "fourth retry is blocked", retriesStarted: 3, wantDelay: 0, wantRetry: false},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			gotDelay, gotRetry := rtspRetryWindow(tc.retriesStarted)
			if gotRetry != tc.wantRetry {
				t.Fatalf("rtspRetryWindow(%d) retry = %t, want %t", tc.retriesStarted, gotRetry, tc.wantRetry)
			}
			if gotDelay != tc.wantDelay {
				t.Fatalf("rtspRetryWindow(%d) delay = %s, want %s", tc.retriesStarted, gotDelay, tc.wantDelay)
			}
		})
	}
}

func TestCameraStreamAutoRestartReason(t *testing.T) {
	now := time.Date(2026, time.April, 14, 20, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		stream         cameraStream
		startupDelay   time.Duration
		wantRestart    bool
		wantReasonPart string
	}{
		{
			name: "rtsp without fps after startup delay restarts",
			stream: cameraStream{
				sourceType: "rtsp",
				cmd:        &exec.Cmd{},
				running:    true,
				status:     "running",
				startTime:  now.Add(-3 * time.Second),
			},
			startupDelay:   2 * time.Second,
			wantRestart:    true,
			wantReasonPart: "no stream progress",
		},
		{
			name: "rtsp with recent progress before fps stays running",
			stream: cameraStream{
				sourceType:     "rtsp",
				cmd:            &exec.Cmd{},
				running:        true,
				status:         "running",
				startTime:      now.Add(-3 * time.Second),
				progressSeen:   true,
				progressSeenAt: now.Add(-500 * time.Millisecond),
			},
			startupDelay: 2 * time.Second,
			wantRestart:  false,
		},
		{
			name: "healthy rtsp stays running",
			stream: cameraStream{
				sourceType: "rtsp",
				cmd:        &exec.Cmd{},
				running:    true,
				status:     "running",
				fps:        "29.97",
				startTime:  now.Add(-10 * time.Second),
			},
			startupDelay: 2 * time.Second,
			wantRestart:  false,
		},
		{
			name: "unexpected rtsp exit triggers recovery",
			stream: cameraStream{
				sourceType: "rtsp",
				status:     "stopped: exit status 1",
				startTime:  now.Add(-5 * time.Second),
			},
			startupDelay:   4 * time.Second,
			wantRestart:    true,
			wantReasonPart: "ffmpeg exited",
		},
		{
			name: "rtsp waits until startup delay expires",
			stream: cameraStream{
				sourceType: "rtsp",
				cmd:        &exec.Cmd{},
				running:    true,
				status:     "running",
				startTime:  now.Add(-1500 * time.Millisecond),
			},
			startupDelay: 2 * time.Second,
			wantRestart:  false,
		},
		{
			name: "non rtsp stream is ignored",
			stream: cameraStream{
				sourceType: "usb",
				cmd:        &exec.Cmd{},
				running:    true,
				status:     "running",
				startTime:  now.Add(-30 * time.Second),
			},
			startupDelay: 2 * time.Second,
			wantRestart:  false,
		},
		{
			name: "intentional stop is ignored",
			stream: cameraStream{
				sourceType: "rtsp",
				status:     "stopped: exit status 1",
				startTime:  now.Add(-10 * time.Second),
				stopping:   true,
			},
			startupDelay: 2 * time.Second,
			wantRestart:  false,
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			reason, ok := tc.stream.autoRestartReason(now, tc.startupDelay)
			if ok != tc.wantRestart {
				t.Fatalf("autoRestartReason() restart = %t, want %t (reason=%q)", ok, tc.wantRestart, reason)
			}
			if tc.wantReasonPart != "" && !strings.Contains(reason, tc.wantReasonPart) {
				t.Fatalf("autoRestartReason() reason = %q, want substring %q", reason, tc.wantReasonPart)
			}
		})
	}
}

func TestMonitoringSourceNeedsRestart(t *testing.T) {
	now := time.Date(2026, time.April, 14, 20, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		spec   sourceSpec
		stream *cameraStream
		want   bool
	}{
		{
			name: "config dirty highlights restart",
			spec: sourceSpec{
				SourceType:   "rtsp",
				Enabled:      true,
				OutputPort:   9005,
				DirtyReasons: []string{"restart"},
			},
			want: true,
		},
		{
			name: "enabled source without stream highlights restart",
			spec: sourceSpec{
				SourceType: "rtsp",
				Enabled:    true,
				OutputPort: 9005,
			},
			want: true,
		},
		{
			name: "rtsp without fps after grace highlights restart",
			spec: sourceSpec{
				SourceType: "rtsp",
				Enabled:    true,
				OutputPort: 9005,
			},
			stream: &cameraStream{
				sourceType: "rtsp",
				cmd:        &exec.Cmd{},
				running:    true,
				status:     "running",
				startTime:  now.Add(-3 * time.Second),
			},
			want: true,
		},
		{
			name: "rtsp with recent progress before fps does not highlight",
			spec: sourceSpec{
				SourceType: "rtsp",
				Enabled:    true,
				OutputPort: 9005,
			},
			stream: &cameraStream{
				sourceType:     "rtsp",
				cmd:            &exec.Cmd{},
				running:        true,
				status:         "running",
				startTime:      now.Add(-3 * time.Second),
				progressSeen:   true,
				progressSeenAt: now.Add(-500 * time.Millisecond),
			},
			want: false,
		},
		{
			name: "rtsp still in startup grace does not highlight",
			spec: sourceSpec{
				SourceType: "rtsp",
				Enabled:    true,
				OutputPort: 9005,
			},
			stream: &cameraStream{
				sourceType: "rtsp",
				cmd:        &exec.Cmd{},
				running:    true,
				status:     "running",
				startTime:  now.Add(-1500 * time.Millisecond),
			},
			want: false,
		},
		{
			name: "healthy rtsp does not highlight",
			spec: sourceSpec{
				SourceType: "rtsp",
				Enabled:    true,
				OutputPort: 9005,
			},
			stream: &cameraStream{
				sourceType: "rtsp",
				cmd:        &exec.Cmd{},
				running:    true,
				status:     "running",
				fps:        "30.00",
				startTime:  now.Add(-5 * time.Second),
			},
			want: false,
		},
		{
			name: "latched timeout stays highlighted during restart grace",
			spec: sourceSpec{
				SourceType: "rtsp",
				Enabled:    true,
				OutputPort: 9005,
			},
			stream: &cameraStream{
				sourceType: "rtsp",
				cmd:        &exec.Cmd{},
				running:    true,
				status:     "running",
				startTime:  now.Add(-1 * time.Second),
			},
			want: true,
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			var recovery *rtspRecoveryState
			if tc.name == "latched timeout stays highlighted during restart grace" {
				recovery = &rtspRecoveryState{attention: true, reason: "no measured FPS after 2s"}
			}
			if got := monitoringSourceNeedsRestart(tc.spec, tc.stream, now, recovery); got != tc.want {
				t.Fatalf("monitoringSourceNeedsRestart() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestPreviewWindowSize(t *testing.T) {
	tests := []struct {
		name       string
		width      int
		height     int
		wantWidth  int
		wantHeight int
		wantOK     bool
	}{
		{name: "portrait 4k is capped", width: 2160, height: 3840, wantWidth: 540, wantHeight: 960, wantOK: true},
		{name: "landscape full hd is capped to half size", width: 1920, height: 1080, wantWidth: 960, wantHeight: 540, wantOK: true},
		{name: "small preview stays native", width: 640, height: 480, wantWidth: 640, wantHeight: 480, wantOK: true},
		{name: "invalid size is rejected", width: 0, height: 480, wantWidth: 0, wantHeight: 0, wantOK: false},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			gotWidth, gotHeight, gotOK := previewWindowSize(tc.width, tc.height)
			if gotOK != tc.wantOK {
				t.Fatalf("previewWindowSize(%d, %d) ok = %t, want %t", tc.width, tc.height, gotOK, tc.wantOK)
			}
			if gotWidth != tc.wantWidth || gotHeight != tc.wantHeight {
				t.Fatalf("previewWindowSize(%d, %d) = %dx%d, want %dx%d", tc.width, tc.height, gotWidth, gotHeight, tc.wantWidth, tc.wantHeight)
			}
		})
	}
}

func TestPreviewArgsForSize(t *testing.T) {
	tests := []struct {
		name string
		size string
		want []string
	}{
		{
			name: "portrait size uses capped window",
			size: "2160x3840",
			want: []string{"-x", "540", "-y", "960"},
		},
		{
			name: "unknown size uses bounded scaling fallback",
			size: "-",
			want: []string{"-x", "960", "-y", "960", "-vf", "scale=960:960:force_original_aspect_ratio=decrease"},
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			got := previewArgsForSize(tc.size)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("previewArgsForSize(%q) = %v, want %v", tc.size, got, tc.want)
			}
		})
	}
}

func TestUnreadyRTSPStatusUsesStartingLabel(t *testing.T) {
	stream := &cameraStream{
		sourceType: "rtsp",
		cmd:        &exec.Cmd{},
		running:    true,
		status:     "running",
		startTime:  time.Now().Add(-1 * time.Second),
	}
	spec := sourceSpec{SourceType: "rtsp"}
	recovery := &rtspRecoveryState{attention: true, reason: "no stream progress after 2s"}

	status := stream.snapshotRow()[8]
	if status != "running" {
		t.Fatalf("snapshot status = %q, want running before UI override", status)
	}

	uiStatus := monitoringSourceStatus(spec, stream, recovery)
	if uiStatus != "starting (1/3)" {
		t.Fatalf("faulted UI status = %q, want starting (1/3)", uiStatus)
	}
}

func TestExitedRTSPStatusUsesStartingLabelWhileRetryPending(t *testing.T) {
	stream := &cameraStream{sourceType: "rtsp", status: "stopped: exit status 1", startTime: time.Now().Add(-5 * time.Second)}
	spec := sourceSpec{SourceType: "rtsp"}
	recovery := &rtspRecoveryState{attention: true, reason: "ffmpeg exited"}

	if got := monitoringSourceStatus(spec, stream, recovery); got != "starting (1/3)" {
		t.Fatalf("monitoringSourceStatus() = %q, want starting (1/3)", got)
	}
}

func TestRetryBackoffRTSPStatusUsesStartingLabel(t *testing.T) {
	spec := sourceSpec{SourceType: "rtsp"}
	recovery := &rtspRecoveryState{attempts: 1, attention: true, nextRetry: time.Now().Add(4 * time.Second)}

	if got := monitoringSourceStatus(spec, nil, recovery); got != "starting (2/3)" {
		t.Fatalf("monitoringSourceStatus() = %q, want starting (2/3)", got)
	}
}

func TestCameraStreamInteractiveReadyAllowsRecentProgress(t *testing.T) {
	stream := &cameraStream{
		sourceType:     "rtsp",
		cmd:            &exec.Cmd{},
		running:        true,
		status:         "running",
		startTime:      time.Now().Add(-3 * time.Second),
		progressSeen:   true,
		progressSeenAt: time.Now().Add(-500 * time.Millisecond),
	}

	if !stream.isInteractiveReady() {
		t.Fatal("isInteractiveReady() = false, want true when recent progress is present")
	}

	stream.running = false
	if stream.isInteractiveReady() {
		t.Fatal("isInteractiveReady() = true, want false for stopped stream")
	}
}

func TestBuildUSBSourcesFromDetectedUsesStableAssignmentState(t *testing.T) {
	previousConfig := camerasConfig
	defer func() {
		camerasConfig = previousConfig
	}()

	camerasConfig = &camerascfg.Config{
		DeviceAssignments: []camerascfg.DeviceAssignment{{
			AttachmentPath: "/stable/device/1",
			MatchKey:       "usb-stable-1",
			Name:           "Platform Left",
			ShortID:        "C1",
			OutputPort:     9001,
			Disabled:       true,
			On:             boolRef(false),
		}},
	}

	cam := recording.DetectedCamera{
		Name:           "USB Camera",
		AttachmentPath: "/stable/device/1",
		MatchKey:       "usb-stable-1",
		Identity:       "USB topology 1",
		PixFmt:         "mjpeg",
		Size:           "1920x1080",
		Fps:            30,
	}

	sources := buildUSBSourcesFromDetected([]recording.DetectedCamera{cam}, 9001, map[int]struct{}{}, map[string]struct{}{})
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if sources[0].Enabled {
		t.Fatal("expected matched stable assignment to set Enabled=false from disabled=true")
	}
	if sources[0].MonitoringOn {
		t.Fatal("expected matched stable assignment to set MonitoringOn=false from on=false")
	}
	if sources[0].Name != "Platform Left" || sources[0].ShortID != "C1" {
		t.Fatalf("expected metadata from stable assignment, got name=%q shortID=%q", sources[0].Name, sources[0].ShortID)
	}
}
