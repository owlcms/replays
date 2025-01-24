package status

const (
	Recording = "REC"  // Recording in progress
	Trimming  = "TRIM" // Trimming video
	Ready     = "DONE" // Video ready
)

// Message wraps a status code and message text
type Message struct {
	Code string `json:"code"`
	Text string `json:"text"`
}
