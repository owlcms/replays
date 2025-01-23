package main

import (
	"flag"
	"fmt"
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

func main() {
	// Parse command-line flags
	flag.BoolVar(&verbose, "v", false, "enable verbose logging")
	flag.BoolVar(&verbose, "verbose", false, "enable verbose logging")
	flag.BoolVar(&noVideo, "noVideo", false, "log ffmpeg actions but do not execute them")
	flag.Parse()

	// Initialize loggers
	logging.Init()

	// Set the noVideo flag in the videos package
	videos.SetNoVideo(noVideo)

	cfg := config.LoadConfig()

	// Set the videoDir and video configuration in the videos package
	videos.SetVideoDir(cfg.VideoDir)
	videos.SetVideoConfig(cfg.Width, cfg.Height, cfg.FPS)

	// Channel to listen for interrupt signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		http.StartServer(cfg.Port, verbose)
	}()

	myApp := app.New()
	window := myApp.NewWindow("OWLCMS Jury Replays")

	label := widget.NewLabel("OWLCMS Jury Replays")
	urlStr := fmt.Sprintf("http://localhost:%d", cfg.Port)
	parsedURL, _ := url.Parse(urlStr)
	hyperlink := widget.NewHyperlink("Open replay list in browser", parsedURL)
	content := container.NewVBox(label, hyperlink)

	window.SetContent(content)
	window.Resize(fyne.NewSize(400, 200))
	window.CenterOnScreen()

	// Show the window before running the application
	window.Show()

	// Goroutine to handle interrupt signals
	go func() {
		<-sigChan
		logging.InfoLogger.Println("Interrupt signal received. Shutting down...")
		http.StopServer()
		myApp.Quit()
	}()

	window.ShowAndRun()
}
