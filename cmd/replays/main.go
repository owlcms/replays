package main

import (
	"fmt"
	"net"
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
	"github.com/owlcms/replays/internal/config/cameras"
	ffmpegcfg "github.com/owlcms/replays/internal/config/ffmpeg"
	"github.com/owlcms/replays/internal/config/replays"
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

func defaultWindowSize() fyne.Size {
	return fyne.NewSize(1480, 880)
}

func getReplayListHost() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		if localAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			ip := localAddr.IP
			if ip != nil && !ip.IsLoopback() {
				if v4 := ip.To4(); v4 != nil {
					return v4.String()
				}
				return ip.String()
			}
		}
	}

	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP == nil || ipNet.IP.IsLoopback() {
				continue
			}
			if v4 := ipNet.IP.To4(); v4 != nil {
				return v4.String()
			}
		}
	}

	return "localhost"
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

	// Set app identity before config resolution
	config.AppName = "replays"

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

	if err := config.ResolveAndEnsureConfigDir(); err != nil {
		fmt.Printf("Failed to initialize config directory: %v\n", err)
		os.Exit(1)
	}

	configPath := filepath.Join(config.GetInstallDir(), "config.toml")
	if err := replays.ExtractDefaultConfig(configPath); err != nil {
		fmt.Printf("Failed to extract config.toml: %v\n", err)
		os.Exit(1)
	}

	// Extract shared ffmpeg.toml (goes to shared config dir, not instance dir)
	if p := ffmpegcfg.ExtractDefaultConfig(); p == "" {
		fmt.Println("Warning: Failed to extract ffmpeg.toml")
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

func formatEnabledCameraList(cfg *replays.Config) string {
	if cfg == nil {
		return "No configuration loaded."
	}

	if len(cfg.Cameras) == 0 {
		if cfg.Multicast.Enabled {
			return "Cameras Module Streams are enabled, but no stream ports are configured."
		}
		return "No enabled cameras are configured for direct input."
	}

	var builder strings.Builder
	if cfg.Multicast.Enabled {
		builder.WriteString("Source mode: Cameras Module Streams\n\n")
	} else {
		builder.WriteString("Source mode: Direct input\n\n")
	}

	for i, camera := range cfg.Cameras {
		builder.WriteString(fmt.Sprintf("%d. %s\n", i+1, camera.FfmpegCamera))
		builder.WriteString(fmt.Sprintf("   Format: %s\n", camera.Format))
		if strings.TrimSpace(camera.Size) != "" {
			builder.WriteString(fmt.Sprintf("   Size: %s\n", camera.Size))
		}
		if camera.Fps > 0 {
			builder.WriteString(fmt.Sprintf("   FPS: %d\n", camera.Fps))
		}
		if strings.TrimSpace(camera.InputParameters) != "" {
			builder.WriteString(fmt.Sprintf("   Input: %s\n", camera.InputParameters))
		}
		if strings.TrimSpace(camera.OutputParameters) != "" {
			builder.WriteString(fmt.Sprintf("   Output: %s\n", camera.OutputParameters))
		}
		builder.WriteString(fmt.Sprintf("   Recode on trim: %t\n", camera.Recode))
		if i < len(cfg.Cameras)-1 {
			builder.WriteString("\n")
		}
	}

	return builder.String()
}

func showEnabledCameras(cfg *replays.Config, window fyne.Window) {
	textArea := widget.NewMultiLineEntry()
	textArea.SetMinRowsVisible(12)
	textArea.SetText(formatEnabledCameraList(cfg))
	textArea.Wrapping = fyne.TextWrapWord

	dialog := dialog.NewCustom("Enabled Cameras", "Close", container.NewVBox(
		widget.NewLabel("Currently active camera sources used by replays:"),
		textArea,
	), window)
	dialog.Resize(fyne.NewSize(640, 420))
	dialog.Show()
}

// showOwlCMSServerAddress shows a dialog with the OwlCMS server address
func showOwlCMSServerAddress(cfg *replays.Config, window fyne.Window) {
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
			if err := replays.UpdateConfigFile(configFilePath, newAddress); err != nil {
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
func showPlatformSelection(cfg *replays.Config, window fyne.Window) {
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
				if err := replays.UpdatePlatform(configFilePath, combo.Selected); err != nil {
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
	cfg := replays.GetCurrentConfig()
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
		_, reloadErr := replays.LoadConfig(configFile)
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

// toggleMulticast flips the Cameras Module Streams flag, saves, and prompts for restart.
func toggleMulticast(cfg *replays.Config, window fyne.Window) {
	cfg.Multicast.Enabled = !cfg.Multicast.Enabled

	configFilePath := filepath.Join(config.GetInstallDir(), "config.toml")
	if err := replays.UpdateMpegTSConfig(configFilePath, cfg.Multicast); err != nil {
		dialog.ShowError(fmt.Errorf("failed to save Cameras Module Stream setting: %w", err), window)
		cfg.Multicast.Enabled = !cfg.Multicast.Enabled // revert
		return
	}

	var msg string
	if cfg.Multicast.Enabled {
		msg = "Cameras Module Streams enabled."
	} else {
		msg = "Cameras Module Streams disabled. Local cameras will be used."
	}
	successDialog := dialog.NewInformation("Success", msg+" The application will now exit. Please restart it.", window)
	successDialog.SetOnClosed(func() {
		window.Close()
		os.Exit(0)
	})
	successDialog.Show()
}

// showMulticastConfig shows a dialog to configure the Cameras Module Stream IP and port mapping.
func showMulticastConfig(cfg *replays.Config, window fyne.Window) {
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
		widget.NewFormItem("Stream IP", ipEntry),
		widget.NewFormItem("Camera 1 port", port1Entry),
		widget.NewFormItem("Camera 2 port", port2Entry),
		widget.NewFormItem("Camera 3 port", port3Entry),
		widget.NewFormItem("Camera 4 port", port4Entry),
	)

	hint := widget.NewLabel("Use a multicast address (e.g. 239.255.0.1) for multicast mode, " +
		"or 0.0.0.0 for unicast mode (passive UDP listener).\n" +
		"If a port is empty, the corresponding camera is disabled.")
	hint.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(form, hint)

	dlg := dialog.NewCustomConfirm("Cameras Module Stream Configuration", "Save", "Cancel", content,
		func(save bool) {
			if !save {
				return
			}

			ip := strings.TrimSpace(ipEntry.Text)
			parsedIP := net.ParseIP(ip)
			if parsedIP == nil {
				dialog.ShowError(fmt.Errorf("stream IP must be a valid IP address (multicast or unicast)"), window)
				return
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

			cfg.Multicast.IP = ip
			cfg.Multicast.Camera1Port = p1
			cfg.Multicast.Camera2Port = p2
			cfg.Multicast.Camera3Port = p3
			cfg.Multicast.Camera4Port = p4

			configFilePath := filepath.Join(config.GetInstallDir(), "config.toml")
			if err := replays.UpdateMpegTSConfig(configFilePath, cfg.Multicast); err != nil {
				dialog.ShowError(fmt.Errorf("failed to save Cameras Module Stream config: %w", err), window)
				return
			}

			mode := "Multicast"
			if !parsedIP.IsMulticast() {
				mode = "Unicast"
			}
			successDialog := dialog.NewInformation("Success", fmt.Sprintf("%s stream configuration saved. The application will now exit. Please restart it.", mode), window)
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
		return "Stop using Cameras Module Streams"
	}
	return "Use Cameras Module Streams"
}

// isUnicastIP reports whether ip is a unicast listen address (0.0.0.0 or a
// non-multicast IP), as opposed to a multicast group address.
func isUnicastIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	return !parsed.IsMulticast()
}

func localMulticastMismatchNote(cfg *replays.Config) string {
	if !cfg.Multicast.Enabled {
		return ""
	}

	replaysIP := strings.TrimSpace(cfg.Multicast.IP)

	// If replays is configured for unicast listening, show an informational note
	if isUnicastIP(replaysIP) {
		logging.InfoLogger.Printf("Replays is in unicast mode (listening on %s)", replaysIP)
		return fmt.Sprintf("Unicast mode: listening on %s. The sending Cameras Module must list this machine in its destinations.", replaysIP)
	}

	camerasCfg, camerasConfigPath, err := loadStartupCamerasConfigForComparison()
	if err != nil {
		logging.WarningLogger.Printf("Skipping cameras/replays multicast check: %v", err)
		return ""
	}

	if camerasConfigPath == "" {
		return ""
	}

	camerasIP := strings.TrimSpace(camerasCfg.Multicast.IP)
	if replaysIP == "" || camerasIP == "" || replaysIP == camerasIP {
		return ""
	}

	logging.WarningLogger.Printf("Replays multicast IP (%s) differs from Cameras multicast IP (%s)", replaysIP, camerasIP)
	return fmt.Sprintf("Warning: local Cameras Module multicast IP is %s, Replays is %s. This is OK when the Cameras Module runs on another machine.", camerasIP, replaysIP)
}

func loadStartupCamerasConfigForComparison() (*cameras.Config, string, error) {
	if config.IsLocalDevRuntime() {
		devDir := filepath.Join(".", config.LocalVideoConfigDir, "cameras")
		cfg, err := cameras.LoadConfigFromDir(devDir)
		if err == nil {
			configPath := filepath.Join(devDir, "config.toml")
			if absPath, absErr := filepath.Abs(configPath); absErr == nil {
				configPath = absPath
			}
			return cfg, configPath, nil
		}
		logging.WarningLogger.Printf("Failed to load local dev Cameras Module config from %s: %v", devDir, err)
	}

	options, err := discoverLocalCamerasVersions()
	if err != nil {
		return nil, "", err
	}
	if len(options) == 0 {
		return nil, "", fmt.Errorf("no local Cameras Module config was found")
	}

	option := options[0]
	cfg, err := cameras.LoadConfigFromDir(option.ConfigDir)
	if err != nil {
		return nil, "", err
	}
	return cfg, option.ConfigPath, nil
}

func main() {
	// Set app identity early (maybeExtractConfigAndExit also sets it)
	config.AppName = "replays"

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
	cfg, err := replays.InitConfig()
	if err != nil {
		logging.ErrorLogger.Printf("Error processing flags or loading config: %v", err)

		// Show error dialog instead of fatal exit
		showConfigError(err, window)

		// Show a minimal window while error dialog is displayed
		content := container.NewPadded(
			widget.NewLabel("Loading configuration..."),
		)
		window.SetContent(content)
		window.Resize(defaultWindowSize())
		window.CenterOnScreen()
		window.Show()
		window.ShowAndRun()
		return
	}

	titleLabel = widget.NewLabel("")
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}
	updateTitle()

	if config.IsLocalDevRuntime() {
		// Ensure shared ffmpeg.toml exists in dev mode
		if p := ffmpegcfg.ExtractDefaultConfig(); p == "" {
			logging.WarningLogger.Println("Failed to ensure default ffmpeg.toml")
		}
	}

	// Initialize FFmpeg path
	if err := recording.InitializeFFmpeg(); err != nil {
		logging.WarningLogger.Printf("Warning: %v", err)
		// Continue execution even if FFmpeg initialization fails
	}
	if err := recording.EnsureCompatibleFFmpegForRecording(cfg.Cameras); err != nil {
		logging.WarningLogger.Printf("Warning: failed to switch to compatible ffmpeg for recording: %v", err)
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
		initialStatus = ""
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
	} else if initialStatus == "" {
		statusLabel.Hide()
	}
	startupMessages := widget.NewLabel("")
	startupMessages.Wrapping = fyne.TextWrapWord
	startupMessages.Hide()

	host := getReplayListHost()
	urlStr := fmt.Sprintf("http://%s:%d", host, cfg.Port)
	parsedURL, _ := url.Parse(urlStr)
	replaysListLabel := widget.NewLabel("Open replay list in browser:")
	hyperlink := widget.NewHyperlink(urlStr, parsedURL)

	upperContent := container.NewVBox(
		topContainer,
		container.NewHBox(replaysListLabel, hyperlink),
		widget.NewSeparator(),
		startupMessages,
		statusLabel,
	)
	content := container.NewPadded(upperContent)

	window.SetContent(content)
	window.Resize(defaultWindowSize())
	window.CenterOnScreen()

	// Create main menu
	multicastToggle := fyne.NewMenuItem(multicastToggleLabel(cfg.Multicast.Enabled), nil)
	multicastToggle.Action = func() {
		toggleMulticast(cfg, window)
	}
	multicastConfigItem := fyne.NewMenuItem("Cameras Module Stream Configuration", func() {
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
			fyne.NewMenuItem("Use Streams from Local Cameras Module", func() {
				showLocalCamerasImportDialog(cfg, window)
			}),
			multicastConfigItem,
			multicastToggle,
			fyne.NewMenuItemSeparator(),
			// Camera tooling
			fyne.NewMenuItem("List Enabled Cameras", func() {
				showEnabledCameras(cfg, window)
			}),
			fyne.NewMenuItem("List Detected Cameras", func() {
				if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
					recording.ListCameras(window)
				}
			}),
			fyne.NewMenuItem("Auto-Detect Hardware", func() {
				if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
					recording.DetectAndWriteConfig(window)
				}
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

			// Skip showing "Reloading..." in the Fyne window
			if msg.Text == "Reloading..." {
				msg.Text = "Ready"
			}

			// Update status text and style
			setStatusLabelText(statusLabel, msg.Text, strings.HasPrefix(msg.Text, "Error:"))

			if msg.Code == httpServer.Ready {
				hideTimer = time.AfterFunc(10*time.Second, func() {
					setStatusLabelText(statusLabel, "Ready", false)
				})
			}
		}
	}()

	// Show the window before running the application
	window.Show()
	startStartupScans(cfg, statusLabel, startupMessages)

	// Set up shutdown hook early in main
	setupShutdownHook()

	// Remove the duplicate signal handling code and replace with window close handler
	window.SetCloseIntercept(func() {
		confirmAndQuit(window)
	})

	window.ShowAndRun()
}
