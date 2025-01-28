//go:build windows || linux

package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
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
)

var sigChan = make(chan os.Signal, 1)

func main() {
	// Disable Fyne telemetry
	os.Setenv("FYNE_TELEMETRY", "0")

	// Process command-line flags and load configuration
	cfg, err := config.InitConfig()
	if err != nil {
		log.Fatalf("Error processing flags: %v", err)
	}

	// Validate camera configuration and set initial status
	var initialStatus string
	if err := cfg.ValidateCamera(); err != nil {
		initialStatus = "Error: " + err.Error()
	} else {
		initialStatus = "Ready"
	}

	// Start HTTP server
	go func() {
		http.StartServer(cfg.Port, config.Verbose)
	}()

	myApp := app.New()
	window := myApp.NewWindow("OWLCMS Jury Replays")

	label := widget.NewLabel("OWLCMS Jury Replays")
	label.TextStyle = fyne.TextStyle{Bold: true}

	urlStr := fmt.Sprintf("http://localhost:%d", cfg.Port)
	parsedURL, _ := url.Parse(urlStr)
	hyperlink := widget.NewHyperlink("Open replay list in browser", parsedURL)
	portLabel := widget.NewLabel(fmt.Sprintf("Port: %d", cfg.Port))

	// Create a horizontal container for the hyperlink and portLabel
	horizontalContainer := container.NewHBox(hyperlink, portLabel)

	// Add status label with initial status (bold for errors)
	statusLabel := widget.NewLabel(initialStatus)
	statusLabel.Wrapping = fyne.TextWrapWord
	if strings.HasPrefix(initialStatus, "Error:") {
		statusLabel.TextStyle = fyne.TextStyle{Bold: true}
	}

	content := container.NewPadded(container.NewVBox(
		label,
		horizontalContainer, // Use the horizontal container here
		widget.NewSeparator(),
		statusLabel,
	))

	window.SetContent(content)
	window.Resize(fyne.NewSize(600, 400))
	window.CenterOnScreen()

	// Create main menu
	mainMenu := fyne.NewMainMenu(
		fyne.NewMenu("Files",
			fyne.NewMenuItem("Open Application Directory", func() {
				openApplicationDirectory()
			}),
		),
	)
	window.SetMainMenu(mainMenu)

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

// openApplicationDirectory opens the application directory in the file explorer
func openApplicationDirectory() {
	dir := config.GetInstallDir()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	case "linux":
		cmd = exec.Command("xdg-open", dir)
	default:
		log.Printf("Unsupported platform: %s", runtime.GOOS)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("Failed to open application directory: %v", err)
	}
}
