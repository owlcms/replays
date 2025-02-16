package logging

import (
	"fmt"
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
	logDir        string
	Verbose       bool // Move Verbose flag here from config package
)

// Trace logs a debug message that only appears when verbose logging is enabled
func Trace(format string, v ...interface{}) {
	if Verbose {
		InfoLogger.Printf(format, v...)
	}
}

// SetVerbose sets the verbose logging flag
func SetVerbose(verbose bool) {
	Verbose = verbose
}

// Init initializes the loggers
func Init(logDirectory string) error {
	logDir = logDirectory

	// Write the value of logDir to the console
	fmt.Printf("Initializing logs in directory: %s\n", logDir)

	// Create logs directory if it doesn't exist
	if err := os.MkdirAll(logDir, os.ModePerm); err != nil {
		fmt.Printf("Failed to create log directory: %v\n", err)
		return err
	}
	fmt.Printf("Log directory created successfully: %s\n", logDir)

	// Open log file with O_SYNC to ensure no buffering
	var err error
	logFile, err = os.OpenFile(filepath.Join(logDir, "replays.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_SYNC, 0666)
	if err != nil {
		return err
	}
	fmt.Printf("Log file created successfully: %s\n", logFile.Name())

	// Initialize writers based on platform
	var infoWriter, warnWriter, errorWriter io.Writer
	if runtime.GOOS == "windows" {
		// Windows: write to file only because of console behavior
		infoWriter = io.MultiWriter(logFile)
		warnWriter = io.MultiWriter(logFile)
		errorWriter = io.MultiWriter(logFile)
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

	return nil
}

// Close closes the log file
func Close() {
	if logFile != nil {
		logFile.Close()
	}
}
