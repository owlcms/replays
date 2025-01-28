//go:build windows || linux

package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/http"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/status"
	"github.com/owlcms/replays/internal/videos"
)

var verbose bool
var noVideo bool
var sigChan = make(chan os.Signal, 1)

func main() {
	// Disable Fyne telemetry
	os.Setenv("FYNE_TELEMETRY", "0")

	// Parse command-line flags
	configFile := flag.String("config", "config.toml", "path to configuration file")
	flag.BoolVar(&verbose, "v", false, "enable verbose logging")
	flag.BoolVar(&verbose, "verbose", false, "enable verbose logging")
	flag.BoolVar(&noVideo, "noVideo", false, "log ffmpeg actions but do not execute them")
	flag.Parse()

	// Initialize loggers
	logging.Init()

	// Load configuration
	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	// Validate camera configuration and set initial status
	var initialStatus string
	if err := cfg.ValidateCamera(); err != nil {
		initialStatus = "Error: " + err.Error()
	} else {
		initialStatus = "Ready"
	}

	// Set the noVideo flag and recode option in the videos package
	videos.SetNoVideo(noVideo)
	videos.SetRecode(cfg.Recode)

	// Start HTTP server
	go func() {
		http.StartServer(cfg.Port, verbose)
	}()

	myApp := app.New()
	window := myApp.NewWindow("OWLCMS Jury Replays")

	label := widget.NewLabel("OWLCMS Jury Replays")
	urlStr := fmt.Sprintf("http://localhost:%d", cfg.Port)
	parsedURL, _ := url.Parse(urlStr)
	hyperlink := widget.NewHyperlink("Open replay list in browser", parsedURL)

	// Add status label with initial status (bold for errors)
	statusLabel := widget.NewLabel(initialStatus)
	statusLabel.Wrapping = fyne.TextWrapWord
	if strings.HasPrefix(initialStatus, "Error:") {
		statusLabel.TextStyle = fyne.TextStyle{Bold: true}
	}

	content := container.NewPadded(container.NewVBox(
		label,
		hyperlink,
		widget.NewSeparator(),
		statusLabel,
	))

	window.SetContent(content)
	window.Resize(fyne.NewSize(400, 200))
	window.CenterOnScreen()

	// Status update goroutine
	go func() {
		var hideTimer *time.Timer
		for msg := range status.StatusChan {
			if hideTimer != nil {
				hideTimer.Stop()
			}

			// Update status text and style
			statusLabel.SetText(msg.Text)
			statusLabel.TextStyle = fyne.TextStyle{
				Bold: strings.HasPrefix(msg.Text, "Error:"),
			}
			statusLabel.Refresh()

			// Auto-hide Ready messages after 10 seconds
			if msg.Code == status.Ready {
				hideTimer = time.AfterFunc(10*time.Second, func() {
					statusLabel.SetText("Ready")
					statusLabel.TextStyle = fyne.TextStyle{Bold: false}
					statusLabel.Refresh()
				})
			}
		}
	}()

	// Show the window before running the application
	window.Show()

	// Initialize signal handling
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Goroutine to handle interrupt signals
	go func() {
		<-sigChan
		logging.InfoLogger.Println("Interrupt signal received. Shutting down...")
		http.StopServer()
		myApp.Quit()
	}()

	window.ShowAndRun()
}
