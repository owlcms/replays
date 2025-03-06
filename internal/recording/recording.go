package recording

import (
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/logging"
)

// Recording tracks whether a recording is currently in progress
var Recording bool

// IsRecording returns the current recording state
func IsRecording() bool {
	return Recording
}

// InitializeFFmpeg finds and stores the ffmpeg path in config
func InitializeFFmpeg() error {
	path := findFFmpeg()
	if path == "ffmpeg.exe" || path == "ffmpeg" {
		// This is the fallback path when nothing else was found
		logging.WarningLogger.Printf("Could not find ffmpeg installation, using fallback path: %s", path)
	}
	config.SetFFmpegPath(path)
	logging.InfoLogger.Printf("FFmpeg executable set to: %s", path)
	return nil
}
