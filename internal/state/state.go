package state

import (
	"encoding/json"
	"log"
	"strings"
	"time"
)

var (
	LastStartTime     int64
	LastTimerStopTime int64
	LastDecisionTime  int64

	// New state variables
	CurrentAthlete  string
	CurrentLiftType string
	CurrentAttempt  int
)

type StartMessage struct {
	AthleteName   string `json:"athleteName"`
	AttemptNumber int    `json:"attemptNumber"`
	LiftType      string `json:"liftType"`
}

func UpdateStateFromStartMessage(message string) {
	// Find the last space to separate JSON and timestamp
	spaceIndex := strings.LastIndex(message, " ")
	if spaceIndex == -1 {
		return
	}

	jsonPart := message[:spaceIndex]
	timePart := message[spaceIndex+1:]

	log.Printf("Received start message: %s", message)
	log.Printf("Parsing json: %s", jsonPart)

	var startMsg StartMessage
	err := json.Unmarshal([]byte(jsonPart), &startMsg)
	if err != nil {
		log.Printf("Error parsing start message: %v", err)
		return
	}

	log.Printf("Parsed start message: %+v", startMsg)

	CurrentAthlete = startMsg.AthleteName
	CurrentAttempt = startMsg.AttemptNumber
	CurrentLiftType = startMsg.LiftType
	LastStartTime = parseTime(timePart)
}

func parseTime(timeStr string) int64 {
	// Implement the time parsing logic here
	// For now, let's assume it returns a dummy value
	return time.Now().UnixNano() / int64(time.Millisecond)
}

func UpdateStateFromStopMessage(message string) {
	LastTimerStopTime = time.Now().UnixNano() / int64(time.Millisecond)
}
