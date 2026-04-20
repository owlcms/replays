package httpServer

import (
	"strings"

	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/state"
)

type StatusCode int

const (
	Ready StatusCode = iota
	Recording
	Trimming
	Error
)

type StatusMessage struct {
	Code          StatusCode `json:"code"`
	Text          string     `json:"text"`
	Session       string     `json:"session"`
	AthleteName   string     `json:"athleteName,omitempty"`
	LiftType      string     `json:"liftType,omitempty"`
	AttemptNumber int        `json:"attemptNumber,omitempty"`
}

var (
	StatusChan          = make(chan StatusMessage, 10)
	statusMsg           string
	statusCode          StatusCode
	VideoReadyReloading bool
)

func buildStatusMessage(code StatusCode, text string) StatusMessage {
	return StatusMessage{
		Code:          code,
		Text:          text,
		Session:       state.CurrentSession,
		AthleteName:   state.CurrentAthlete,
		LiftType:      state.CurrentLiftType,
		AttemptNumber: state.CurrentAttempt,
	}
}

// SendStatus sends a status update to all clients through the broadcast channel
// and updates the Fyne UI through StatusChan
func SendStatus(code StatusCode, text string) {
	// Simplify the "Videos ready" message for web display
	VideoReadyReloading = false
	if code == Ready && strings.Contains(text, "Videos ready") {
		text = "Reloading..."
		VideoReadyReloading = true
	}
	msg := buildStatusMessage(code, text)
	mu.Lock()
	statusMsg = text
	for client := range clients {
		logging.InfoLogger.Printf("Sending status update: %s", text)
		if err := client.WriteJSON(msg); err != nil {
			logging.ErrorLogger.Printf("Failed to send status: %v", err)
			client.Close()
			delete(clients, client)
			continue
		}
	}
	mu.Unlock()

	// Also send to Fyne UI
	StatusChan <- msg
}
