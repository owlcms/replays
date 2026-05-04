package recording

import (
	"testing"

	ffmpegcfg "github.com/owlcms/replays/internal/config/ffmpeg"
)

func testFFmpegConfigWithEncoder(name string) *ffmpegcfg.Config {
	return &ffmpegcfg.Config{
		Encoders: []ffmpegcfg.EncoderConfig{{Name: name}},
	}
}

func TestParseAvailableH264EncodersOnlyUsesEncoderRows(t *testing.T) {
	output := []byte(`Encoders:
 V..... h264_nvenc           NVIDIA NVENC H.264 encoder (codec h264)
 V....D libx264              H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 (codec h264)
 configuration: --enable-h264_amf
 V..... wrapped_description  mentions h264_amf but is not that encoder
 V....D h264_amf             AMD AMF H.264 Encoder (codec h264)
 A..... h264_audio_name      not a video encoder
 V..... h264_qsv             H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 (Intel Quick Sync Video acceleration)
`)

	available := parseAvailableH264Encoders(output)
	for _, name := range []string{"h264_nvenc", "h264_amf", "h264_qsv"} {
		if !available[name] {
			t.Fatalf("available[%q] = false, want true", name)
		}
	}
	for _, name := range []string{"libx264", "h264_audio_name", "wrapped_description"} {
		if available[name] {
			t.Fatalf("available[%q] = true, want false", name)
		}
	}
}

func TestEncoderGPUVendorMatches(t *testing.T) {
	tests := []struct {
		name     string
		required []string
		detected map[string]bool
		want     bool
	}{
		{
			name:     "matches detected vendor",
			required: []string{" AMD "},
			detected: map[string]bool{"amd": true},
			want:     true,
		},
		{
			name:     "skips when detected vendors do not overlap",
			required: []string{"amd"},
			detected: map[string]bool{"intel": true, "nvidia": true},
			want:     false,
		},
		{
			name:     "skips vendor encoder when GPU detection found nothing",
			required: []string{"amd"},
			detected: map[string]bool{},
			want:     false,
		},
		{
			name:     "allows encoder without vendor requirements when GPU detection found nothing",
			required: nil,
			detected: map[string]bool{},
			want:     true,
		},
		{
			name:     "allows encoders without vendor requirements",
			required: nil,
			detected: map[string]bool{"intel": true},
			want:     true,
		},
	}

	for i := range tests {
		tc := tests[i]
		t.Run(tc.name, func(t *testing.T) {
			if got := encoderGPUVendorMatches(tc.required, tc.detected); got != tc.want {
				t.Fatalf("encoderGPUVendorMatches(%v, %v) = %t, want %t", tc.required, tc.detected, got, tc.want)
			}
		})
	}
}

func TestEncoderIsProbeCandidateRequiresAvailableEncoderAndMatchingGPU(t *testing.T) {
	available := map[string]bool{"h264_nvenc": true, "h264_amf": true}
	detected := map[string]bool{"intel": true, "nvidia": true}

	if !encoderIsProbeCandidate("h264_nvenc", []string{"nvidia"}, available, detected) {
		t.Fatal("h264_nvenc should be a candidate when ffmpeg reports it and NVIDIA is detected")
	}
	if encoderIsProbeCandidate("h264_amf", []string{"amd"}, available, detected) {
		t.Fatal("h264_amf should not be a candidate when only Intel/NVIDIA GPUs are detected")
	}
	if encoderIsProbeCandidate("h264_qsv", []string{"intel"}, available, detected) {
		t.Fatal("h264_qsv should not be a candidate when ffmpeg did not report it")
	}
}

func TestReportUnconfiguredEncodersUsesProgress(t *testing.T) {
	available := map[string]bool{"h264_amf": true, "h264_nvenc": true, "h264_qsv": true}
	cfg := testFFmpegConfigWithEncoder("h264_qsv")
	var messages []string

	reportUnconfiguredEncoders(available, cfg, func(message string) {
		messages = append(messages, message)
	})

	want := []string{
		ProgressMsg(ProgEncoderUnconfigured, ProgressDetailPayload("h264_amf", "no ffmpeg.toml encoder settings")),
		ProgressMsg(ProgEncoderUnconfigured, ProgressDetailPayload("h264_nvenc", "no ffmpeg.toml encoder settings")),
	}
	if len(messages) != len(want) {
		t.Fatalf("reported %d messages, want %d: %v", len(messages), len(want), messages)
	}
	for i := range want {
		if messages[i] != want[i] {
			t.Fatalf("message[%d] = %q, want %q", i, messages[i], want[i])
		}
	}
}
