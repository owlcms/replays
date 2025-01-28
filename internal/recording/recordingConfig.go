package recording

import (
	"runtime"
)

// Configuration variables exported for use within the recording package
var (
	FfmpegPath   string
	FfmpegCamera string
	FfmpegFormat string
	NoVideo      bool
	VideoDir     string
	Width        int
	Height       int
	Fps          int
)

// SetNoVideo sets the noVideo flag
func SetNoVideo(value bool) {
	NoVideo = value
}

// SetVideoDir sets the video directory
func SetVideoDir(dir string) {
	VideoDir = dir
}

// SetVideoConfig sets the video configuration parameters
func SetVideoConfig(w, h, f int) {
	Width = w
	Height = h
	Fps = f
}

// SetFfmpegConfig sets the ffmpeg configuration parameters
func SetFfmpegConfig(path, camera, format string) {
	if path == "" {
		if runtime.GOOS == "windows" {
			FfmpegPath = "ffmpeg.exe"
		} else {
			FfmpegPath = "ffmpeg"
		}
	} else {
		FfmpegPath = path
	}
	FfmpegCamera = camera
	FfmpegFormat = format
}
