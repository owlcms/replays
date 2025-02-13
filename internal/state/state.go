package state

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/owlcms/replays/internal/logging"
)

var (
	LastStartTime     int64
	LastTimerStopTime int64
	LastDecisionTime  int64

	// New state variables
	CurrentAthlete      string
	CurrentLiftType     string
	CurrentAttempt      int
	StopRequestCount    int
	CurrentCameraNumber int
	CurrentSession      string // Current competition session name
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
	StopRequestCount = 0
}

func UpdateStateFromStopMessage(message string) {
	StopRequestCount++
	if StopRequestCount == 1 {
		LastTimerStopTime = time.Now().UnixNano() / int64(time.Millisecond)
		logging.InfoLogger.Println("Stop time recorded")
	}

}

func parseTime(_ string) int64 {
	// Implement the time parsing logic here
	// For now, let's assume it returns a dummy value
	return time.Now().UnixNano() / int64(time.Millisecond)
}
