package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/owlcms/replays/internal/assets"
	"github.com/owlcms/replays/internal/config"
	"github.com/owlcms/replays/internal/downloadutils"
	"github.com/owlcms/replays/internal/httpServer"
	"github.com/owlcms/replays/internal/logging"
	"github.com/owlcms/replays/internal/monitor"
	"github.com/owlcms/replays/internal/recording"
)

var titleLabel *widget.Label

func setAppIcon(myApp fyne.App) {
	if assets.IconResource != nil && len(assets.IconResource.Content()) > 0 {
		myApp.SetIcon(assets.IconResource)
		return
	}

	iconCandidates := make([]string, 0, 2)

	if exePath, err := os.Executable(); err == nil {
		iconCandidates = append(iconCandidates, filepath.Join(filepath.Dir(exePath), "Icon.png"))
	}
	if wd, err := os.Getwd(); err == nil {
		iconCandidates = append(iconCandidates, filepath.Join(wd, "Icon.png"))
	}

	for _, iconPath := range iconCandidates {
		res, err := fyne.LoadResourceFromPath(iconPath)
		if err == nil {
			myApp.SetIcon(res)
			return
		}
	}
}

func maybeExtractConfigAndExit() bool {
	var extract bool
	for _, arg := range os.Args[1:] {
		if arg == "--extractConfig" {
			extract = true
			break
		}
	}
	if !extract {
		return false
	}

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--configDir" && i+1 < len(os.Args) {
			config.ConfigDir = os.Args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(arg, "--configDir=") {
			config.ConfigDir = strings.TrimPrefix(arg, "--configDir=")
		}
	}

	if config.ConfigDir != "" {
		if absConfigDir, err := filepath.Abs(config.ConfigDir); err == nil {
			config.ConfigDir = absConfigDir
		}
	}

	configPath := filepath.Join(config.GetInstallDir(), "config.toml")
	if err := config.ExtractDefaultConfig(configPath); err != nil {
		fmt.Printf("Failed to extract config.toml: %v\n", err)
		os.Exit(1)
	}

	multicastPath := filepath.Join(config.GetInstallDir(), "multicast.toml")
	if err := config.ExtractDefaultMulticastConfig(multicastPath); err != nil {
		fmt.Printf("Failed to extract multicast.toml: %v\n", err)
		os.Exit(1)
	}

	if p := config.ExtractDefaultCamerasConfig(); p == "" {
		fmt.Println("Failed to extract camera config files")
		os.Exit(1)
	}

	fmt.Printf("Extracted config files in: %s\n", config.GetInstallDir())
	return true
}

// shutdown gracefully shuts down all services
func shutdown() {
	logging.InfoLogger.Println("Shutting down application...")

	// Stop any ongoing recordings
	recording.TerminateRecordings()

	// Stop HTTP server
	httpServer.StopServer()

	// Disconnect MQTT
	monitor.DisconnectMQTT()

	logging.InfoLogger.Println("Application shutdown complete")
}

// setupShutdownHook sets up signal handling for graceful shutdown
func setupShutdownHook() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		logging.InfoLogger.Println("Interrupt signal received. Shutting down...")
		shutdown()
		os.Exit(0)
	}()
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
			successDialog := dialog.NewInformation("Success", "OwlCMS server address updated. The application will now exit. Please restart it.", window)
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
	// Use the stored validated platforms if available
	platforms := monitor.GetStoredPlatforms()

	// If no platforms are stored, try to request them
	if len(platforms) == 0 {
		// Check if we have a server connection before trying to request platforms
		if cfg.OwlCMS == "" {
			dialog.ShowInformation("No Server Connection", "Please configure the owlcms server address first.", window)
			return
		}

		// Request fresh platform list
		monitor.PublishConfig(cfg.Platform)

		// Wait up to 2 seconds for response
		select {
		case platforms = <-monitor.PlatformListChan:
			// got platforms
		case <-time.After(2 * time.Second):
			dialog.ShowInformation("Not Available", "No response from owlcms server. Please check server connection.", window)
			return
		}
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
				successDialog := dialog.NewInformation("Success", "Platform updated. The application will now exit. Please restart it.", window)
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
	// Check if MQTT is connected - if not, shutdown immediately
	if !monitor.IsConnected() {
		logging.InfoLogger.Println("No MQTT connection - shutting down immediately")
		shutdown()
		window.Close()
		return
	}

	confirmDialog := dialog.NewConfirm(
		"Confirm Exit",
		"Are you sure you want to exit? This will stop jury replays. Any ongoing recordings will be stopped.",
		func(confirm bool) {
			if confirm {
				logging.InfoLogger.Println("User requested application shutdown")
				shutdown()
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

// showConfigError displays configuration errors in a dialog and allows user to fix them
func showConfigError(err error, window fyne.Window) {
	errorMsg := fmt.Sprintf("Configuration Error:\n\n%v\n\nPlease check your config.toml file and fix the error, then click 'Retry' to reload the configuration.", err)

	// Use a scrollable text widget instead of a label
	content := widget.NewEntry()
	content.SetText(errorMsg)
	content.MultiLine = true
	content.Wrapping = fyne.TextWrapWord
	scrollableContent := container.NewScroll(content)
	scrollableContent.SetMinSize(fyne.NewSize(640, 200))

	var configDialog dialog.Dialog
	retryBtn := widget.NewButton("Retry", func() {
		// Attempt to reload configuration
		configFile := filepath.Join(config.GetInstallDir(), "config.toml")
		_, reloadErr := config.LoadConfig(configFile)
		if reloadErr != nil {
			// Still has errors, show again
			configDialog.Hide()
			showConfigError(reloadErr, window)
			return
		}

		// Configuration loaded successfully, restart the application
		dialog.ShowInformation("Success", "Configuration loaded successfully. The application will now exit. Please restart it.", window)
		time.AfterFunc(2*time.Second, func() {
			window.Close()
			os.Exit(0)
		})
	})

	openConfigBtn := widget.NewButton("Open Config File", func() {
		openConfigFile()
	})

	exitBtn := widget.NewButton("Exit", func() {
		configDialog.Hide()
	})

	buttonContainer := container.NewHBox(retryBtn, openConfigBtn, exitBtn)

	dialogContent := container.NewVBox(
		scrollableContent,
		widget.NewSeparator(),
		buttonContainer,
	)

	configDialog = dialog.NewCustom("Configuration Error", "", dialogContent, window)
	configDialog.SetOnClosed(func() {
		// User chose to exit instead of fixing
		os.Exit(1)
	})
	configDialog.Resize(fyne.NewSize(690, 340))
	configDialog.Show()
}

// openConfigFile opens the config.toml file in the default editor
func openConfigFile() {
	configPath := filepath.Join(config.GetInstallDir(), "config.toml")
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("notepad", configPath)
	case "darwin":
		cmd = exec.Command("open", "-t", configPath)
	case "linux":
		// Try common editors
		editors := []string{"xdg-open", "gedit", "kate", "nano", "vim"}
		for _, editor := range editors {
			if _, err := exec.LookPath(editor); err == nil {
				cmd = exec.Command(editor, configPath)
				break
			}
		}
	}

	if cmd != nil {
		if err := cmd.Start(); err != nil {
			logging.ErrorLogger.Printf("Failed to open config file: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Multicast camera helpers
// ---------------------------------------------------------------------------

// toggleMulticast flips the multicast enabled flag, saves, and prompts for restart.
func toggleMulticast(cfg *config.Config, window fyne.Window) {
	cfg.Multicast.Enabled = !cfg.Multicast.Enabled

	configFilePath := filepath.Join(config.GetInstallDir(), "config.toml")
	if err := config.UpdateMulticastConfig(configFilePath, cfg.Multicast); err != nil {
		dialog.ShowError(fmt.Errorf("failed to save multicast setting: %w", err), window)
		cfg.Multicast.Enabled = !cfg.Multicast.Enabled // revert
		return
	}

	var msg string
	if cfg.Multicast.Enabled {
		msg = "Multicast cameras enabled."
	} else {
		msg = "Multicast cameras disabled. Local cameras will be used."
	}
	successDialog := dialog.NewInformation("Success", msg+" The application will now exit. Please restart it.", window)
	successDialog.SetOnClosed(func() {
		window.Close()
		os.Exit(0)
	})
	successDialog.Show()
}

// showMulticastConfig shows a dialog to configure the multicast IP and port mapping.
func showMulticastConfig(cfg *config.Config, window fyne.Window) {
	m := cfg.Multicast
	portToText := func(port int) string {
		if port <= 0 {
			return ""
		}
		return strconv.Itoa(port)
	}
	parseOptionalPort := func(name, text string) (int, error) {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return 0, nil
		}
		port, err := strconv.Atoi(trimmed)
		if err != nil || port < 1 || port > 65535 {
			return 0, fmt.Errorf("%s must be empty or a number between 1 and 65535", name)
		}
		return port, nil
	}

	multicastPath := filepath.Join(config.GetInstallDir(), "multicast.toml")
	if err := config.ExtractDefaultMulticastConfig(multicastPath); err == nil {
		if loaded, err := config.LoadMulticastConfig(multicastPath); err == nil {
			m.IP = loaded.IP
			m.Camera1Port = loaded.Camera1Port
			m.Camera2Port = loaded.Camera2Port
			m.Camera3Port = loaded.Camera3Port
			m.Camera4Port = loaded.Camera4Port
		}
	}

	ipEntry := widget.NewEntry()
	ipEntry.SetText(m.IP)

	port1Entry := widget.NewEntry()
	port1Entry.SetText(portToText(m.Camera1Port))
	port2Entry := widget.NewEntry()
	port2Entry.SetText(portToText(m.Camera2Port))
	port3Entry := widget.NewEntry()
	port3Entry.SetText(portToText(m.Camera3Port))
	port4Entry := widget.NewEntry()
	port4Entry.SetText(portToText(m.Camera4Port))

	form := widget.NewForm(
		widget.NewFormItem("Multicast IP", ipEntry),
		widget.NewFormItem("Camera 1 port", port1Entry),
		widget.NewFormItem("Camera 2 port", port2Entry),
		widget.NewFormItem("Camera 3 port", port3Entry),
		widget.NewFormItem("Camera 4 port", port4Entry),
	)

	hint := widget.NewLabel("Clear to turn off. If a port is empty, the corresponding multicast camera is turned off.")
	hint.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(form, hint)

	dlg := dialog.NewCustomConfirm("Configure Multicast Mapping", "Save", "Cancel", content,
		func(save bool) {
			if !save {
				return
			}

			ip := strings.TrimSpace(ipEntry.Text)
			if ip == "" {
				ip = "239.255.0.1"
			}
			p1, err := parseOptionalPort("Camera 1 port", port1Entry.Text)
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			p2, err := parseOptionalPort("Camera 2 port", port2Entry.Text)
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			p3, err := parseOptionalPort("Camera 3 port", port3Entry.Text)
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			p4, err := parseOptionalPort("Camera 4 port", port4Entry.Text)
			if err != nil {
				dialog.ShowError(err, window)
				return
			}

			m.IP = ip
			m.Camera1Port = p1
			m.Camera2Port = p2
			m.Camera3Port = p3
			m.Camera4Port = p4

			cfg.Multicast.IP = m.IP
			cfg.Multicast.Camera1Port = m.Camera1Port
			cfg.Multicast.Camera2Port = m.Camera2Port
			cfg.Multicast.Camera3Port = m.Camera3Port
			cfg.Multicast.Camera4Port = m.Camera4Port

			if err := config.UpdateMulticastMappingFile(multicastPath, m); err != nil {
				dialog.ShowError(fmt.Errorf("failed to save multicast config: %w", err), window)
				return
			}

			successDialog := dialog.NewInformation("Success", "Multicast mapping updated in multicast.toml. The application will now exit. Please restart it.", window)
			successDialog.SetOnClosed(func() {
				window.Close()
				os.Exit(0)
			})
			successDialog.Show()
		}, window)
	dlg.Resize(fyne.NewSize(400, 0))
	dlg.Show()
}

// multicastToggleLabel returns the menu label text based on current state.
func multicastToggleLabel(enabled bool) string {
	if enabled {
		return "Stop using Multicast"
	}
	return "Use Multicast Cameras"
}

func main() {
	if maybeExtractConfigAndExit() {
		return
	}

	// Disable Fyne telemetry
	os.Setenv("FYNE_TELEMETRY", "0")

	// Create the Fyne app and window first
	myApp := app.New()
	setAppIcon(myApp)
	window := myApp.NewWindow("OWLCMS Jury Replays")

	// Process command-line flags and load configuration
	cfg, err := config.InitConfig()
	if err != nil {
		logging.ErrorLogger.Printf("Error processing flags or loading config: %v", err)

		// Show error dialog instead of fatal exit
		showConfigError(err, window)

		// Show a minimal window while error dialog is displayed
		content := container.NewPadded(
			widget.NewLabel("Loading configuration..."),
		)
		window.SetContent(content)
		window.Resize(fyne.NewSize(900, 400))
		window.CenterOnScreen()
		window.Show()
		window.ShowAndRun()
		return
	}

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
	multicastToggle := fyne.NewMenuItem(multicastToggleLabel(cfg.Multicast.Enabled), nil)
	multicastToggle.Action = func() {
		toggleMulticast(cfg, window)
	}
	multicastConfigItem := fyne.NewMenuItem("Configure Multicast Mapping", func() {
		showMulticastConfig(cfg, window)
	})

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
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Quit", func() {
				confirmAndQuit(window)
			}),
		),
		fyne.NewMenu("Cameras",
			multicastToggle,
			multicastConfigItem,
		),
		fyne.NewMenu("Help",
			// Add "List Cameras" menu item for Windows and Linux
			fyne.NewMenuItem("List Cameras", func() {
				if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
					recording.ListCameras(window)
				}
			}),
			fyne.NewMenuItem("Auto-Detect Hardware", func() {
				if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
					recording.DetectAndWriteConfig(window)
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
	if config.NoMQTT {
		logging.InfoLogger.Println("MQTT autodiscovery disabled via -noMQTT flag")
		statusLabel.SetText("MQTT disabled")
	} else {
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
	}

	// Set up shutdown hook early in main
	setupShutdownHook()

	// Remove the duplicate signal handling code and replace with window close handler
	window.SetCloseIntercept(func() {
		confirmAndQuit(window)
	})

	window.ShowAndRun()
}
