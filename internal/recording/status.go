package recording

import (
	"encoding/json"

	"github.com/owlcms/replays/internal/status"
	"github.com/owlcms/replays/internal/websocket"
)

// SendStatus sends a status update with code and message
func SendStatus(code string, text string) {
	msg := status.Message{
		Code: code,
		Text: text,
	}

	// Send to web clients
	if data, err := json.Marshal(msg); err == nil {
		websocket.SendMessage(string(data))

		// Only trigger page reload when video is ready
		if code == status.Ready {
			websocket.SendMessage("reload")
		}
	}

	// Send to Fyne UI
	select {
	case status.StatusChan <- msg:
	default:
		// Channel is full, skip this update
	}
}
