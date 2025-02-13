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
	Code    StatusCode `json:"code"`
	Text    string     `json:"text"`
	Session string     `json:"session"` // Add session field
}

var (
	StatusChan = make(chan StatusMessage, 10)
	statusMsg  string
)

// SendStatus sends a status update to all clients through the broadcast channel
// and updates the Fyne UI through StatusChan
func SendStatus(code StatusCode, text string) {
	// Simplify the "Videos ready" message for web display
	if code == Ready && strings.Contains(text, "Videos ready") {
		text = "Videos ready"
	}
	msg := StatusMessage{
		Code:    code,
		Text:    text,
		Session: state.CurrentSession, // Include current session in message
	}
	logging.InfoLogger.Printf("Sending status update: %s", text)

	mu.Lock()
	statusMsg = text
	for client := range clients {
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
