package videos

import (
	"os"
	"path/filepath"
	"runtime"
)

// Configuration variables exported for use within the videos package
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
			exePath, err := os.Executable()
			if err == nil {
				FfmpegPath = filepath.Join(filepath.Dir(exePath), "hiddenffmpeg.exe")
			} else {
				FfmpegPath = "ffmpeg"
			}
		} else {
			FfmpegPath = "ffmpeg"
		}
	} else {
		FfmpegPath = path
	}
	FfmpegCamera = camera
	FfmpegFormat = format
}
