//go:build windows || linux

package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/http"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/monitor"
	"github.com/owlcms/replays/internal/status"
)

var sigChan = make(chan os.Signal, 1)

func main() {
	// Disable Fyne telemetry
	os.Setenv("FYNE_TELEMETRY", "0")

	// Process command-line flags and load configuration
	cfg, err := config.InitConfig()
	if err != nil {
		logging.ErrorLogger.Fatalf("Error processing flags: %v", err)
	}

	// Initialize with an empty status
	var initialStatus string
	if err := cfg.ValidateCamera(); err != nil {
		initialStatus = "Error: " + err.Error()
	} else {
		initialStatus = "Scanning for owlcms server..."
	}

	// Start HTTP server
	go func() {
		http.StartServer(cfg.Port, config.Verbose)
	}()

	myApp := app.New()
	window := myApp.NewWindow("OWLCMS Jury Replays")

	window.SetCloseIntercept(func() {
		confirmDialog := dialog.NewConfirm(
			"Confirm Exit",
			"The replays recorder is running. This will stop jury recordings. Are you sure you want to exit?",
			func(confirm bool) {
				if !confirm {
					logging.ErrorLogger.Println("Closing replays recorder")
					window.Close()
				}
			},
			window,
		)
		confirmDialog.SetConfirmText("Don't Stop Recorder")
		confirmDialog.SetDismissText("Stop Recorder and Exit")
		confirmDialog.Show()
	})

	label := widget.NewLabel("OWLCMS Jury Replays")
	label.TextStyle = fyne.TextStyle{Bold: true}

	urlStr := fmt.Sprintf("http://localhost:%d", cfg.Port)
	parsedURL, _ := url.Parse(urlStr)
	hyperlink := widget.NewHyperlink("Open replay list in browser", parsedURL)

	// Create a horizontal container for the hyperlink
	horizontalContainer := container.NewHBox(hyperlink)

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
			fyne.NewMenuItem("owlcms Server Address", func() {
				showOwlCMSServerAddress(cfg, window)
			}),
			fyne.NewMenuItem("Open Application Directory", func() {
				openApplicationDirectory()
			}),
		),
		fyne.NewMenu("Help",
			fyne.NewMenuItem("About", func() {
				dialog.ShowInformation("About", fmt.Sprintf("OWLCMS Jury Replays\nVersion %s", config.GetProgramVersion()), window)
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

	// Discover or verify MQTT broker after window is shown
	go func() {
		broker, err := monitor.UpdateOwlcmsAddress(cfg, filepath.Join(config.GetInstallDir(), "config.toml"))
		if err != nil {
			logging.ErrorLogger.Printf("Failed to find MQTT broker: %v", err)
			statusLabel.SetText(fmt.Sprintf("Error: Could not find owlcms server - %v", err))
			statusLabel.TextStyle = fyne.TextStyle{Bold: true}
			statusLabel.Refresh()
		} else {
			cfg.OwlCMS = broker
			statusLabel.SetText("Ready")
			statusLabel.TextStyle = fyne.TextStyle{Bold: false}
			statusLabel.Refresh()

			// Start MQTT monitor only after successful discovery
			go monitor.Monitor(cfg)
		}
	}()

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
		logging.WarningLogger.Printf("Unsupported platform: %s", runtime.GOOS)
		return
	}
	if err := cmd.Start(); err != nil {
		logging.ErrorLogger.Printf("Failed to open application directory: %v", err)
	}
}

// showOwlCMSServerAddress shows a dialog with the OwlCMS server address
func showOwlCMSServerAddress(cfg *config.Config, window fyne.Window) {
	var message string
	if cfg.OwlCMS == "" {
		message = "No server located."
	} else {
		message = fmt.Sprintf("Current owlcms Server Address:\n%s", cfg.OwlCMS)
	}

	entry := widget.NewEntry()
	entry.SetPlaceHolder("Enter new server address")

	updateFunc := func() {
		newAddress := entry.Text
		if newAddress != "" {
			cfg.OwlCMS = newAddress
			configFilePath := filepath.Join(config.GetInstallDir(), "config.toml")
			if err := config.UpdateConfigFile(configFilePath, newAddress); err != nil {
				fmt.Printf("Error updating config file: %v\n", err)
				dialog.ShowError(err, window)
				return
			}
			successDialog := dialog.NewInformation("Success", "OwlCMS server address updated. Restart the application.", window)
			successDialog.SetOnClosed(func() {
				window.Close()
				os.Exit(0)
			})
			successDialog.Show()
		}
	}

	// Set the entry's OnSubmitted handler
	entry.OnSubmitted = func(string) { updateFunc() }

	form := container.NewBorder(
		nil,
		nil,
		nil,
		widget.NewButton("Update", updateFunc),
		entry,
	)

	content := container.NewVBox(
		widget.NewLabel(message),
		form,
	)
	dialog := dialog.NewCustom("OwlCMS Server Address", "Close", content, window)
	dialog.Resize(fyne.NewSize(400, 0))
	dialog.Show()
}
