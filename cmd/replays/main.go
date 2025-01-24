package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/http"
	"github.com/owlcms/replays/internal/logging"
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

	// Set the noVideo flag in the videos package
	videos.SetNoVideo(noVideo)

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

	// Add status label
	statusLabel := widget.NewLabel("Ready")
	content := container.NewVBox(
		label,
		hyperlink,
		widget.NewSeparator(),
		statusLabel,
	)

	window.SetContent(content)
	window.Resize(fyne.NewSize(400, 200))
	window.CenterOnScreen()

	// Status update goroutine
	go func() {
		for status := range videos.StatusChan {
			// Update status in UI thread
			statusLabel.SetText(status)
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
