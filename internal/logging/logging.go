package logging

import (
	"io"
	"log"
	"os"
)

var (
	// InfoLogger is used for informational messages
	InfoLogger *log.Logger
	// WarningLogger is used for warning messages
	WarningLogger *log.Logger
	// ErrorLogger is used for error messages
	ErrorLogger *log.Logger
)

// Init initializes the loggers
func Init() {
	// Create or append to log file
	logFile, err := os.OpenFile("replays.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal("Failed to open log file: ", err)
	}

	// Create multi-writers that write to both console and file
	infoWriter := io.MultiWriter(os.Stdout, logFile)
	errorWriter := io.MultiWriter(os.Stderr, logFile)

	// Initialize loggers with appropriate prefixes and flags
	InfoLogger = log.New(infoWriter, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	WarningLogger = log.New(infoWriter, "WARNING: ", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLogger = log.New(errorWriter, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
}
