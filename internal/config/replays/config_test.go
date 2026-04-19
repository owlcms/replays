package replays

import (
	"testing"

	configpkg "github.com/owlcms/replays/internal/config"
)

func TestMergeCameraConfigurationsAppendsManualSources(t *testing.T) {
	autoCameras := []configpkg.CameraConfiguration{
		{Format: "dshow", FfmpegCamera: "video=UHA-UTCA"},
		{Format: "dshow", FfmpegCamera: "video=Integrated Webcam"},
	}
	manualCameras := []configpkg.CameraConfiguration{
		{Format: "rtsp", FfmpegCamera: "rtsp://192.168.1.160:8080/video/h264"},
	}

	merged := mergeCameraConfigurations(autoCameras, manualCameras)
	if len(merged) != 3 {
		t.Fatalf("expected 3 merged cameras, got %d", len(merged))
	}
	if merged[2].FfmpegCamera != manualCameras[0].FfmpegCamera {
		t.Fatalf("expected manual camera appended at end, got %q", merged[2].FfmpegCamera)
	}
}

func TestMergeCameraConfigurationsManualOverridesSameSource(t *testing.T) {
	autoCameras := []configpkg.CameraConfiguration{
		{
			Format:           "rtsp",
			FfmpegCamera:     "rtsp://192.168.1.160:8080/video/h264",
			InputParameters:  "-rtsp_transport udp",
			OutputParameters: "-c:v copy -an",
		},
	}
	manualCameras := []configpkg.CameraConfiguration{
		{
			Format:           "rtsp",
			FfmpegCamera:     "rtsp://192.168.1.160:8080/video/h264",
			InputParameters:  "-rtsp_transport tcp",
			OutputParameters: "-c:v copy -an",
		},
	}

	merged := mergeCameraConfigurations(autoCameras, manualCameras)
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged camera, got %d", len(merged))
	}
	if merged[0].InputParameters != "-rtsp_transport tcp" {
		t.Fatalf("expected manual camera to override auto camera, got %q", merged[0].InputParameters)
	}
}