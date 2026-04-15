package cameras

import "testing"

func TestEnsureSourceIDsKeepsRTSPSourcesUnique(t *testing.T) {
	cfg := &Config{
		RTSPSources: []RTSPSource{
			{RTSPURL: "rtsp://192.168.1.10:8554/live", Transport: "tcp"},
			{RTSPURL: "rtsp://192.168.1.10:8554/live", Transport: "udp"},
			{SourceID: "custom-source", RTSPURL: "rtsp://192.168.1.11:8554/live", Transport: "tcp"},
			{SourceID: "custom-source", RTSPURL: "rtsp://192.168.1.12:8554/live", Transport: "tcp"},
		},
	}

	cfg.ensureSourceIDs()

	seen := make(map[string]struct{}, len(cfg.RTSPSources))
	for i, src := range cfg.RTSPSources {
		if src.SourceID == "" {
			t.Fatalf("source %d has empty source ID", i)
		}
		if _, exists := seen[src.SourceID]; exists {
			t.Fatalf("source %d reused duplicate source ID %q", i, src.SourceID)
		}
		seen[src.SourceID] = struct{}{}
	}

	if cfg.RTSPSources[2].SourceID != "custom-source" {
		t.Fatalf("expected first explicit source ID to be preserved, got %q", cfg.RTSPSources[2].SourceID)
	}
	if cfg.RTSPSources[3].SourceID == "custom-source" {
		t.Fatalf("expected duplicate explicit source ID to be reassigned")
	}
	if cfg.RTSPSources[0].SourceID == cfg.RTSPSources[1].SourceID {
		t.Fatalf("expected duplicate RTSP URLs to receive unique source IDs")
	}
}

func TestApplyDefaultsMarksUnprobedRTSPSourceDirty(t *testing.T) {
	cfg := &Config{
		RTSPSources: []RTSPSource{
			{RTSPURL: "rtsp://192.168.1.10:8554/live"},
			{RTSPURL: "rtsp://192.168.1.11:8554/live", Codec: "h264", ProbeSize: "1920x1080", ProbeFPS: 30},
		},
	}

	cfg.applyDefaults()

	if !cfg.RTSPSources[0].ProbeDirty {
		t.Fatalf("expected unprobed RTSP source to be marked dirty")
	}
	if cfg.RTSPSources[1].ProbeDirty {
		t.Fatalf("expected previously probed RTSP source to stay clean")
	}
}

func TestApplyDefaultsMarksUnprobedDeviceAssignmentDirty(t *testing.T) {
	cfg := &Config{
		DeviceAssignments: []DeviceAssignment{
			{MatchKey: "usb-1"},
			{MatchKey: "usb-2", ProbePixelFormat: "mjpeg", ProbeSize: "1920x1080", ProbeFPS: 30},
		},
	}

	cfg.applyDefaults()

	if len(cfg.DeviceAssignments[0].DirtyReasons) == 0 || cfg.DeviceAssignments[0].DirtyReasons[0] != "probe" {
		t.Fatalf("expected unprobed device assignment to be marked dirty, got %v", cfg.DeviceAssignments[0].DirtyReasons)
	}
	if len(cfg.DeviceAssignments[1].DirtyReasons) != 0 {
		t.Fatalf("expected previously probed device assignment to stay clean, got %v", cfg.DeviceAssignments[1].DirtyReasons)
	}
}
