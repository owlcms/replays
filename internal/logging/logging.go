package logging

import (
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
	InfoLogger = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	WarningLogger = log.New(os.Stdout, "WARNING: ", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLogger = log.New(os.Stderr, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
}
