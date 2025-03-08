package main

import (
	"bufio"
	"bytes"
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
	"github.com/owlcms/replays/internal/downloadutils"
	"github.com/owlcms/replays/internal/httpServer"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/monitor"
	"github.com/owlcms/replays/internal/recording"
)

var sigChan = make(chan os.Signal, 1)
var titleLabel *widget.Label

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
				recording.TerminateRecordings()

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

func updateTitle() {
	cfg := config.GetCurrentConfig()
	platform := cfg.Platform
	if platform == "" {
		platform = "No Platform Selected"
	}
	titleLabel.SetText(fmt.Sprintf("OWLCMS Jury Replays - Platform %s", platform))
}

// listCameras lists available cameras using ffmpeg on Windows or v4l2-ctl on Linux and displays them in a Fyne text area
func listCameras(window fyne.Window) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		args := []string{"-list_devices", "true", "-f", "dshow", "-i", "dummy", "-hide_banner"}
		cmd = recording.CreateFfmpegCmd(args)
	} else if runtime.GOOS == "linux" {
		cmd = exec.Command("v4l2-ctl", "--list-devices")
	} else {
		dialog.ShowInformation("Unsupported Platform", "Camera listing is not supported on this platform.", window)
		return
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		logging.ErrorLogger.Printf("Failed to list cameras: %v", err)
		dialog.ShowError(err, window)
		return
	}

	var cameraNames []string
	scanner := bufio.NewScanner(&out)
	if runtime.GOOS == "windows" {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "(video)") {
				start := strings.Index(line, "\"")
				end := strings.LastIndex(line, "\"")
				if start != -1 && end != -1 && start != end {
					cameraNames = append(cameraNames, line[start+1:end])
				}
			}
		}
	} else if runtime.GOOS == "linux" {
		var currentCamera string
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "(usb-") {
				currentCamera = strings.TrimSpace(line)
			} else if strings.HasPrefix(line, "/dev/video") && currentCamera != "" {
				cameraNames = append(cameraNames, fmt.Sprintf("%s: %s", currentCamera, strings.TrimSpace(line)))
				currentCamera = ""
			}
		}
	}

	if len(cameraNames) == 0 {
		dialog.ShowInformation("No Cameras Found", "No cameras were found on this system.", window)
		return
	}

	cameraList := strings.Join(cameraNames, "\n")
	textArea := widget.NewMultiLineEntry()
	textArea.SetText(cameraList)
	textArea.Wrapping = fyne.TextWrapWord

	dialog := dialog.NewCustom("Available Cameras", "Close", container.NewVBox(
		widget.NewLabel("The following cameras were found on this system:"),
		textArea,
	), window)
	dialog.Resize(fyne.NewSize(400, 300))
	dialog.Show()
}

func main() {
	// Disable Fyne telemetry
	os.Setenv("FYNE_TELEMETRY", "0")

	// Process command-line flags and load configuration
	cfg, err := config.InitConfig()
	if err != nil {
		logging.ErrorLogger.Fatalf("Error processing flags: %v", err)
	}

	myApp := app.New()
	window := myApp.NewWindow("OWLCMS Jury Replays")

	titleLabel = widget.NewLabel("")
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}
	updateTitle()

	// Check for ffmpeg directory on Windows
	if runtime.GOOS == "windows" {
		installDir := config.GetInstallDir()
		ffmpegDir := filepath.Join(installDir, recording.FfmpegBuild)
		if _, err := os.Stat(ffmpegDir); os.IsNotExist(err) {
			// Directory does not exist, download and extract ffmpeg
			downloadURL := downloadutils.GetDownloadURL()
			destPath := filepath.Join(installDir, "ffmpeg.zip")

			progressDialog := dialog.NewProgress("Downloading FFmpeg", "Please wait while FFmpeg is being downloaded...", window)
			progressDialog.Show()

			cancel := make(chan bool)
			go func() {
				err := downloadutils.DownloadArchive(downloadURL, destPath, func(downloaded, total int64) {
					progressDialog.SetValue(float64(downloaded) / float64(total))
				}, cancel)
				if err != nil {
					logging.ErrorLogger.Printf("Failed to download FFmpeg: %v", err)
					dialog.ShowError(err, window)
					progressDialog.Hide()
					return
				}

				err = downloadutils.ExtractZip(destPath, installDir)
				if err != nil {
					logging.ErrorLogger.Printf("Failed to extract FFmpeg: %v", err)
					dialog.ShowError(err, window)
					progressDialog.Hide()
					return
				}

				progressDialog.Hide()
				//dialog.ShowInformation("Success", "FFmpeg has been downloaded and extracted successfully.", window)
			}()
		}
	}

	// Initialize FFmpeg path
	if err := recording.InitializeFFmpeg(); err != nil {
		logging.WarningLogger.Printf("Warning: %v", err)
		// Continue execution even if FFmpeg initialization fails
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

	label := widget.NewLabel("OWLCMS Jury Replays")
	label.TextStyle = fyne.TextStyle{Bold: true}

	// Create containers
	topContainer := container.NewVBox(titleLabel)

	// Add status label with initial status (bold for errors)
	statusLabel := widget.NewLabel(initialStatus)
	statusLabel.Wrapping = fyne.TextWrapWord
	if strings.HasPrefix(initialStatus, "Error:") {
		statusLabel.TextStyle = fyne.TextStyle{Bold: true}
	}

	urlStr := fmt.Sprintf("http://localhost:%d", cfg.Port)
	parsedURL, _ := url.Parse(urlStr)
	hyperlink := widget.NewHyperlink("Open replay list in browser", parsedURL)

	content := container.NewPadded(container.NewVBox(
		topContainer,
		container.NewHBox(hyperlink),
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
				updateTitle() // Update title after platform selection
			}),
			fyne.NewMenuItem("owlcms Server Address", func() {
				showOwlCMSServerAddress(cfg, window)
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Open Application Directory", func() {
				openApplicationDirectory()
			}),

			fyne.NewMenuItem("Quit", func() {
				confirmAndQuit(window)
			}),
		),
		fyne.NewMenu("Help",
			// Add "List Cameras" menu item for Windows and Linux
			fyne.NewMenuItem("List Cameras", func() {
				if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
					listCameras(window)
				}
			}),
			fyne.NewMenuItemSeparator(),
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

			// Skip showing "Reloading..." in the Fyne window
			if msg.Text == "Reloading..." {
				msg.Text = "Ready"
			}

			// Update status text and style
			statusLabel.SetText(msg.Text)
			statusLabel.TextStyle = fyne.TextStyle{
				Bold: strings.HasPrefix(msg.Text, "Error:"),
			}
			statusLabel.Refresh()

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

			// Start MQTT monitor which handles platform list retrieval
			go monitor.Monitor(cfg)
		}
	}()

	// Initialize signal handling
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Goroutine to handle interrupt signals
	go func() {
		<-sigChan
		logging.InfoLogger.Println("Interrupt signal received. Shutting down...")
		recording.TerminateRecordings()
		httpServer.StopServer()
		myApp.Quit()
	}()

	window.ShowAndRun()
}
