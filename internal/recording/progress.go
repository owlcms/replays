package recording

import "strings"

// ProgressSep is the separator between a progress tag and its payload.
// Using a rare Unicode character so it cannot appear in device names or paths.
const ProgressSep = "¤"

// Progress tags for messages emitted by this package via ProbeProgressFunc.
// Each message is formatted as tag + ProgressSep + payload.
// Callers should use ProgressMsg to build messages and ProgressParse to decode them.
const (
	// ProgLocalSource is emitted while examining a local (USB/V4L2/DirectShow) source.
	// Payload: source name.
	ProgLocalSource = "localSrc"

	// ProgEncoder is emitted while examining a hardware encoder candidate.
	// Payload: encoder name.
	ProgEncoder = "encoder"

	// ProgListing is emitted when starting a device enumeration pass.
	// Payload: human-readable description (not parsed structurally).
	ProgListing = "listing"

	// ProgHwMsg is emitted for general hardware encoder status messages.
	// Payload: human-readable description (not parsed structurally).
	ProgHwMsg = "hwMsg"
)

// ProgressMsg builds a tagged progress message for use with ProbeProgressFunc.
func ProgressMsg(tag, payload string) string {
	return tag + ProgressSep + payload
}

// ProgressParse splits a tagged progress message into (tag, payload, ok).
// Returns ok=false if the message does not contain the separator.
func ProgressParse(msg string) (tag, payload string, ok bool) {
	tag, payload, ok = strings.Cut(msg, ProgressSep)
	return
}
