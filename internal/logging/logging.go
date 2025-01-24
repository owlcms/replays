package logging

import (
	"io"
	"log"
	"os"
	"runtime"
)

var (
	InfoLogger    *log.Logger
	WarningLogger *log.Logger
	ErrorLogger   *log.Logger
	logFile       *os.File
)

// Init initializes the loggers
func Init() {
	// Create or append to log file
	var err error
	logFile, err = os.OpenFile("replays.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal("Failed to open log file: ", err)
	}

	// Initialize writers based on platform
	var infoWriter, warnWriter, errorWriter io.Writer
	if runtime.GOOS == "windows" {
		// Windows: write only to file
		infoWriter = logFile
		warnWriter = logFile
		errorWriter = logFile
	} else {
		// Linux/WSL: write to both console and file
		infoWriter = io.MultiWriter(os.Stdout, logFile)
		warnWriter = io.MultiWriter(os.Stdout, logFile)
		errorWriter = io.MultiWriter(os.Stderr, logFile)
	}

	// Initialize loggers with timestamps and source file info
	flags := log.Ldate | log.Ltime | log.Lshortfile
	InfoLogger = log.New(infoWriter, "INFO: ", flags)
	WarningLogger = log.New(warnWriter, "WARN: ", flags)
	ErrorLogger = log.New(errorWriter, "ERROR: ", flags)

	// Register cleanup on program exit
	if cleanup := os.Getenv("CLEANUP_ON_EXIT"); cleanup != "" {
		defer logFile.Close()
	}
}
