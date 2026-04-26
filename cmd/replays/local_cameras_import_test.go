package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	camerascfg "github.com/owlcms/replays/internal/config/cameras"
)

func TestDiscoverLocalCamerasVersionsInDirSortsNewestFirst(t *testing.T) {
	root := t.TempDir()
	versions := []string{"2.9.0", "2.10.0", "2.3.4"}
	for _, version := range versions {
		configDir := filepath.Join(root, version)
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", version, err)
		}
		if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("[multicast]\nstartPort = 9001\n"), 0o644); err != nil {
			t.Fatalf("write config.toml for %s: %v", version, err)
		}
	}

	options, err := discoverLocalCamerasVersionsInDir(root)
	if err != nil {
		t.Fatalf("discover versions: %v", err)
	}
	if len(options) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(options))
	}
	if options[0].Version != "2.10.0" || options[1].Version != "2.9.0" || options[2].Version != "2.3.4" {
		t.Fatalf("unexpected version order: %#v", options)
	}
}

func TestDiscoverLocalCamerasVersionsUsesEnvOverride(t *testing.T) {
	overrideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(overrideDir, "config.toml"), []byte("[multicast]\nstartPort = 9001\n"), 0o644); err != nil {
		t.Fatalf("write override config.toml: %v", err)
	}
	t.Setenv(localCamerasDirOverrideEnv, overrideDir)

	options, err := discoverLocalCamerasVersions()
	if err != nil {
		t.Fatalf("discover versions with env override: %v", err)
	}
	if len(options) != 1 {
		t.Fatalf("expected 1 override option, got %d", len(options))
	}
	if options[0].ConfigDir != overrideDir {
		t.Fatalf("expected override dir %q, got %q", overrideDir, options[0].ConfigDir)
	}
	if options[0].ConfigPath != filepath.Join(overrideDir, "config.toml") {
		t.Fatalf("expected override config path %q, got %q", filepath.Join(overrideDir, "config.toml"), options[0].ConfigPath)
	}
}

func TestLoadLocalCamerasImportPreviewUsesPassiveListenerForUnicast(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.toml")
	content := `
[multicast]
ip = "239.255.0.1"
startPort = 9001

[unicast]
enabled = true
startPort = 9001

[[unicast.destinations]]
address = "127.0.0.1"
enabled = true

[[deviceAssignment]]
matchKey = "usb-1"
name = "Platform Right"
shortId = "C2"
outputPort = 9002
on = true

[[deviceAssignment]]
matchKey = "usb-2"
name = "Platform Left"
shortId = "C1"
outputPort = 9001
on = true

[[rtsp]]
sourceId = "rtsp-a"
name = "Side Angle"
shortId = "R1"
enabled = true
on = true
rtspUrl = "rtsp://127.0.0.1:8554/side"
outputPort = 9005
transport = "tcp"
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	preview, err := loadLocalCamerasImportPreview(localCamerasVersionOption{
		Label:      "2.10.0",
		Version:    "2.10.0",
		ConfigDir:  configDir,
		ConfigPath: configPath,
	})
	if err != nil {
		t.Fatalf("load import preview: %v", err)
	}

	if preview.Mode != "unicast" {
		t.Fatalf("expected unicast mode, got %q", preview.Mode)
	}
	if preview.ListenIP != "0.0.0.0" {
		t.Fatalf("expected passive listener IP 0.0.0.0, got %q", preview.ListenIP)
	}
	if !preview.CompatibilityAllowed {
		t.Fatalf("expected unicast preview to be compatible, got message %q", preview.CompatibilityMessage)
	}
	if preview.CamerasAddressLabel != "Cameras unicast address" {
		t.Fatalf("unexpected cameras address label: %q", preview.CamerasAddressLabel)
	}
	if preview.CamerasAddressValue != "127.0.0.1" {
		t.Fatalf("expected matched local unicast address 127.0.0.1, got %q", preview.CamerasAddressValue)
	}
	if preview.ReplaysAddressValue != "0.0.0.0" {
		t.Fatalf("expected replays listening value 0.0.0.0, got %q", preview.ReplaysAddressValue)
	}
	if len(preview.ImportedStreams) != 3 {
		t.Fatalf("expected 3 imported streams, got %d", len(preview.ImportedStreams))
	}
	if preview.ImportedStreams[0].ShortID != "C1" || preview.ImportedStreams[1].ShortID != "C2" || preview.ImportedStreams[2].ShortID != "R1" {
		t.Fatalf("unexpected stream order: %#v", preview.ImportedStreams)
	}
	if len(preview.EnabledDestinations) != 1 || preview.EnabledDestinations[0] != "127.0.0.1" {
		t.Fatalf("unexpected enabled destinations: %#v", preview.EnabledDestinations)
	}
}

func TestLoadLocalCamerasImportPreviewFlagsUnicastWithoutLocalDestination(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.toml")
	content := `
[multicast]
ip = "239.255.0.1"
startPort = 9001

[unicast]
enabled = true
startPort = 9001

[[unicast.destinations]]
address = "203.0.113.10"
enabled = true

[[rtsp]]
sourceId = "rtsp-a"
name = "Remote Angle"
shortId = "R1"
enabled = true
on = true
rtspUrl = "rtsp://203.0.113.10:8554/remote"
outputPort = 9005
transport = "tcp"
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	preview, err := loadLocalCamerasImportPreview(localCamerasVersionOption{
		Label:      "2.10.0",
		Version:    "2.10.0",
		ConfigDir:  configDir,
		ConfigPath: configPath,
	})
	if err != nil {
		t.Fatalf("load import preview: %v", err)
	}

	if preview.CompatibilityAllowed {
		t.Fatalf("expected unicast preview to be incompatible")
	}
	if preview.CamerasAddressValue != "203.0.113.10" {
		t.Fatalf("expected cameras unicast value 203.0.113.10, got %q", preview.CamerasAddressValue)
	}
	if !strings.Contains(preview.CompatibilityMessage, "does not allow capturing") {
		t.Fatalf("expected incompatibility message, got %q", preview.CompatibilityMessage)
	}
}

func TestCollectLocalCamerasStreamsSkipsDisabledSources(t *testing.T) {
	cfg := &camerascfg.Config{
		DeviceAssignments: []camerascfg.DeviceAssignment{
			{Name: "Enabled USB", ShortID: "C1", OutputPort: 9001},
			{Name: "Disabled USB", ShortID: "C2", OutputPort: 9002, Disabled: true},
		},
		RTSPSources: []camerascfg.RTSPSource{
			{Name: "Enabled RTSP", ShortID: "R1", OutputPort: 9005, Enabled: true},
			{Name: "Disabled RTSP", ShortID: "R2", OutputPort: 9006, Enabled: false},
		},
	}

	streams := collectLocalCamerasStreams(cfg)
	if len(streams) != 2 {
		t.Fatalf("expected 2 streams, got %d", len(streams))
	}
	if streams[0].ShortID != "C1" || streams[1].ShortID != "R1" {
		t.Fatalf("unexpected collected streams: %#v", streams)
	}
}
