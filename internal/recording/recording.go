package recording

// Recording tracks whether a recording is currently in progress
var Recording bool

// IsRecording returns the current recording state
func IsRecording() bool {
	return Recording
}
