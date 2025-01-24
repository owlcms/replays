package state

var (
	LastStartTime     int64
	LastTimerStopTime int64
	LastDecisionTime  int64

	// New state variables
	CurrentAthlete  string
	CurrentLiftType string
	CurrentAttempt  int
)
