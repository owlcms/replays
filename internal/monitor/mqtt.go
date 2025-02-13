package monitor

import (
    "encoding/json"
    "logging"
)

// ...other imports...

func processMessage(topic string, payload []byte) {
    // ...existing code...

    switch topic {
    case "weightlifting/attempt/current":
        var msg CurrentAttemptMessage
        if err := json.Unmarshal(payload, &msg); err != nil {
            logging.ErrorLogger.Printf("Error unmarshaling current attempt message: %v", err)
            return
        }
        state.CurrentAthlete = msg.Athlete
        state.CurrentAttempt = msg.Attempt
        state.CurrentLiftType = msg.LiftType
        state.CurrentSession = msg.Session // Store session name
        
    // ...rest of existing code...
}
