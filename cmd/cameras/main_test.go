package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"
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
			wantReasonPart: "no measured FPS",
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
