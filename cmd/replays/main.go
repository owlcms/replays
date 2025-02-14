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
	"github.com/owlcms/replays/internal/httpServer"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/monitor"
	"github.com/owlcms/replays/internal/recording"
)

var sigChan = make(chan os.Signal, 1)

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

// showPlatformSelection shows a dialog with platform selection dropdown
func showPlatformSelection(cfg *config.Config, window fyne.Window) {
	// Request fresh platform list
	monitor.PublishConfig(cfg.Platform)

	// Wait up to 2 seconds for response
	var platforms []string
	select {
	case platforms = <-monitor.PlatformListChan:
		// got platforms
	case <-time.After(2 * time.Second):
		dialog.ShowInformation("Not Available", "No response from owlcms server", window)
		return
	}

	if len(platforms) == 0 {
		dialog.ShowInformation("No Platforms", "No platforms configured on owlcms server", window)
		return
	}

	combo := widget.NewSelect(platforms, nil)
	// Only set the current platform if it exists in the list
	for _, p := range platforms {
		if p == cfg.Platform {
			combo.SetSelected(cfg.Platform)
			break
		}
	}

	content := container.NewVBox(
		widget.NewLabel("Select Platform:"),
		combo,
	)

	dialog := dialog.NewCustomConfirm("Platform Selection", "Update", "Cancel", content,
		func(update bool) {
			if update && combo.Selected != "" {
				cfg.Platform = combo.Selected
				configFilePath := filepath.Join(config.GetInstallDir(), "config.toml")
				if err := config.UpdatePlatform(configFilePath, combo.Selected); err != nil {
					dialog.ShowError(err, window)
					return
				}
				successDialog := dialog.NewInformation("Success", "Platform updated. Restart the application.", window)
				successDialog.SetOnClosed(func() {
					window.Close()
					os.Exit(0)
				})
				successDialog.Show()
			}
		}, window)
	dialog.Resize(fyne.NewSize(300, 0))
	dialog.Show()
}

// confirmAndQuit shows a confirmation dialog and quits if confirmed
func confirmAndQuit(window fyne.Window) {
	confirmDialog := dialog.NewConfirm(
		"Confirm Exit",
		"Are you sure you want to exit? This will stop jury replays. Any ongoing recordings will be stopped.",
		func(confirm bool) {
			if confirm {
				logging.InfoLogger.Println("Forced closing of replays recorder")

				// Stop any ongoing recordings
				recording.ForceStopRecordings()

				httpServer.StopServer()

				// Close the window
				window.Close()
			}
		},
		window,
	)
	confirmDialog.SetDismissText("Cancel")
	confirmDialog.SetConfirmText("Exit")
	confirmDialog.Show()
}

func main() {
	// Disable Fyne telemetry
	os.Setenv("FYNE_TELEMETRY", "0")

	// Process command-line flags and load configuration
	cfg, err := config.InitConfig()
	if err != nil {
		logging.ErrorLogger.Fatalf("Error processing flags: %v", err)
	}

	// Set recording package configuration
	recording.SetNoVideo(config.NoVideo)
	recording.SetVideoDir(cfg.VideoDir)
	recording.SetVideoConfig(cfg.Width, cfg.Height, cfg.Fps)

	// Initialize with an empty status
	var initialStatus string
	if err := cfg.ValidateCamera(); err != nil {
		initialStatus = "Error: " + err.Error()
	} else {
		initialStatus = "Scanning for owlcms server..."
	}

	// Start HTTP server
	go func() {
		httpServer.StartServer(cfg.Port, config.Verbose)
	}()

	myApp := app.New()
	window := myApp.NewWindow("OWLCMS Jury Replays")

	window.SetCloseIntercept(func() {
		confirmAndQuit(window)
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
		fyne.NewMenu("File",
			fyne.NewMenuItem("Platform Selection", func() {
				showPlatformSelection(cfg, window)
			}),
			fyne.NewMenuItem("owlcms Server Address", func() {
				showOwlCMSServerAddress(cfg, window)
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Open Application Directory", func() {
				openApplicationDirectory()
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Quit", func() {
				confirmAndQuit(window)
			}),
		),
		fyne.NewMenu("Help",
			fyne.NewMenuItem("About", func() {
				dialog.ShowInformation("About", fmt.Sprintf("OWLCMS Jury Replays\nVersion %s", config.GetProgramVersion()), window)
			}),
		),
	)
	window.SetMainMenu(mainMenu)

	// Register platform dialog function for monitor package
	monitor.ShowPlatformDialogFunc = func() {
		showPlatformSelection(cfg, window)
	}

	// Status update goroutine
	go func() {
		var hideTimer *time.Timer
		for msg := range httpServer.StatusChan {
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
			if msg.Code == httpServer.Ready {
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
		recording.ForceStopRecordings()
		httpServer.StopServer()
		myApp.Quit()
	}()

	window.ShowAndRun()
}
