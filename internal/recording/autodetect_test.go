package recording

import "testing"

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
			name:     "allows probe when GPU detection found nothing",
			required: []string{"amd"},
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
