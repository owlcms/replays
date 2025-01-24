package logging

import (
	"io"
	"log"
	"os"
	"path/filepath"
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
	// Create logs directory if it doesn't exist
	if err := os.MkdirAll("logs", os.ModePerm); err != nil {
		log.Fatal(err)
	}

	// Open log file
	var err error
	logFile, err = os.OpenFile(filepath.Join("logs", "replays.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal("Failed to open log file: ", err)
	}

	// Initialize writers based on platform
	var infoWriter, warnWriter, errorWriter io.Writer
	if runtime.GOOS == "windows" {
		// Windows: write to both console and file
		multiWriter := io.MultiWriter(os.Stdout, logFile)
		infoWriter = multiWriter
		warnWriter = multiWriter
		errorWriter = multiWriter
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
