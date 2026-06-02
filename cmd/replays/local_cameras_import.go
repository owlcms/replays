package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/owlcms/replays/internal/config"
	camerascfg "github.com/owlcms/replays/internal/config/cameras"
	replayscfg "github.com/owlcms/replays/internal/config/replays"
)

const maxImportedCameraStreams = 4
const localCamerasDirOverrideEnv = "VIDEO_CAMERAS_DIR"

type localCamerasVersionOption struct {
	Label      string
	Version    string
	ConfigDir  string
	ConfigPath string
}

type localCamerasStream struct {
	Name       string
	ShortID    string
	OutputPort int
	Kind       string
}

type localCamerasImportPreview struct {
	Version              localCamerasVersionOption
	Mode                 string
	ListenIP             string
	CompatibilityAllowed bool
	CompatibilityMessage string
	CamerasAddressLabel  string
	CamerasAddressValue  string
	ReplaysAddressValue  string
	LocalAddresses       []string
	EnabledDestinations  []string
	OrderedStreams       []localCamerasStream
	ImportedStreams      []localCamerasStream
	AdditionalStreams    []localCamerasStream
}

type localCamerasSelectionRow struct {
	stream localCamerasStream
	check  *widget.Check
}

// localCamerasInstallDir returns the owlcms-cameras installation directory.
// In production it is derived from the install layout (sibling of owlcms-replays).
// Returns "" if in production mode and no sibling cameras install is found —
// that signals a remote-only deployment where cameras runs on another machine.
func localCamerasInstallDir() string {
	// In production, replays runs from <root>/owlcms-replays/<version>/.
	// Cameras is installed at the sibling <root>/owlcms-cameras/.
	// Derive that path from GetInstallDir() so it tracks wherever the user
	// installed the apps, rather than relying on a hardcoded $HOME-based path.
	if !config.IsLocalDevRuntime() {
		// GetInstallDir() = <root>/owlcms-replays/<version>
		// filepath.Dir twice → <root>
		root := filepath.Dir(filepath.Dir(config.GetInstallDir()))
		sibling := filepath.Join(root, "owlcms-cameras")
		if info, err := os.Stat(sibling); err == nil && info.IsDir() {
			return sibling
		}
		// Production mode, no sibling: cameras is on another machine.
		return ""
	}
	// Dev: use OS default install location (then dev fallback in discoverLocalCamerasVersions).
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "owlcms-cameras")
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "owlcms-cameras")
	case "linux":
		return filepath.Join(os.Getenv("HOME"), ".local", "share", "owlcms-cameras")
	default:
		return "./owlcms-cameras"
	}
}

func localCamerasOverrideOption() (*localCamerasVersionOption, error) {
	raw := strings.TrimSpace(os.Getenv(localCamerasDirOverrideEnv))
	if raw == "" {
		return nil, nil
	}

	resolved := raw
	if absPath, err := filepath.Abs(raw); err == nil {
		resolved = absPath
	}

	configDir := resolved
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("%s=%s: %w", localCamerasDirOverrideEnv, raw, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s=%s: expected a Cameras version directory", localCamerasDirOverrideEnv, raw)
	}

	configPath := filepath.Join(configDir, "config.toml")
	configInfo, err := os.Stat(configPath)
	if err != nil {
		return nil, fmt.Errorf("%s=%s: missing config.toml: %w", localCamerasDirOverrideEnv, raw, err)
	}
	if configInfo.IsDir() {
		return nil, fmt.Errorf("%s=%s: config.toml is a directory", localCamerasDirOverrideEnv, raw)
	}

	version := filepath.Base(configDir)
	if strings.TrimSpace(version) == "" || version == "." || version == string(filepath.Separator) {
		version = "override"
	}

	return &localCamerasVersionOption{
		Label:      version + " (env)",
		Version:    version,
		ConfigDir:  configDir,
		ConfigPath: configPath,
	}, nil
}

// errRemoteCamerasOnly is returned when replays is in production mode with no
// local Cameras Module installation — cameras runs on a separate machine.
var errRemoteCamerasOnly = fmt.Errorf("no local Cameras Module installation found: this Replays instance appears to be receiving streams from a remote Cameras Module; use the stream configuration menu to set ports instead")

func discoverLocalCamerasVersions() ([]localCamerasVersionOption, error) {
	override, err := localCamerasOverrideOption()
	if err != nil {
		return nil, err
	}
	if override != nil {
		return []localCamerasVersionOption{*override}, nil
	}

	// When replays itself is running from its local dev runtime
	// (./video_config/replays, no control-panel VIDEO_CONFIGDIR), prefer the
	// sibling dev cameras config so a production install does not shadow it.
	if config.IsLocalDevRuntime() {
		if dev := devLocalCamerasOption(); dev != nil {
			return []localCamerasVersionOption{*dev}, nil
		}
	}

	installDir := localCamerasInstallDir()
	if installDir == "" {
		// Production mode, no sibling cameras install: remote use case.
		return nil, errRemoteCamerasOnly
	}

	options, err := discoverLocalCamerasVersionsInDir(installDir)
	if err != nil {
		return nil, err
	}
	if len(options) > 0 {
		return options, nil
	}

	if dev := devLocalCamerasOption(); dev != nil {
		return []localCamerasVersionOption{*dev}, nil
	}

	return nil, nil
}

// devLocalCamerasOption returns the local dev cameras config option
// (./video_config/cameras/config.toml) if it exists, or nil otherwise.
func devLocalCamerasOption() *localCamerasVersionOption {
	devDir, err := filepath.Abs(filepath.Join(".", config.LocalVideoConfigDir, "cameras"))
	if err != nil {
		return nil
	}
	configPath := filepath.Join(devDir, "config.toml")
	if info, statErr := os.Stat(configPath); statErr == nil && !info.IsDir() {
		return &localCamerasVersionOption{
			Label:      "local-dev",
			Version:    "local-dev",
			ConfigDir:  devDir,
			ConfigPath: configPath,
		}
	}
	return nil
}

func discoverLocalCamerasVersionsInDir(root string) ([]localCamerasVersionOption, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	options := make([]localCamerasVersionOption, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		configDir := filepath.Join(root, entry.Name())
		configPath := filepath.Join(configDir, "config.toml")
		info, statErr := os.Stat(configPath)
		if statErr != nil || info.IsDir() {
			continue
		}

		options = append(options, localCamerasVersionOption{
			Label:      entry.Name(),
			Version:    entry.Name(),
			ConfigDir:  configDir,
			ConfigPath: configPath,
		})
	}

	sort.Slice(options, func(i, j int) bool {
		return compareVersionLabels(options[i].Version, options[j].Version) > 0
	})

	return options, nil
}

func compareVersionLabels(left, right string) int {
	left = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(left), "v"))
	right = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(right), "v"))

	leftParts, leftOK := parseVersionParts(left)
	rightParts, rightOK := parseVersionParts(right)
	if leftOK && rightOK {
		count := len(leftParts)
		if len(rightParts) > count {
			count = len(rightParts)
		}
		for i := 0; i < count; i++ {
			leftValue := 0
			if i < len(leftParts) {
				leftValue = leftParts[i]
			}
			rightValue := 0
			if i < len(rightParts) {
				rightValue = rightParts[i]
			}
			if leftValue < rightValue {
				return -1
			}
			if leftValue > rightValue {
				return 1
			}
		}
	}

	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func parseVersionParts(raw string) ([]int, bool) {
	if raw == "" {
		return nil, false
	}
	parts := strings.Split(raw, ".")
	values := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

func monitoringEnabled(value *bool) bool {
	return value == nil || *value
}

func collectLocalCamerasStreams(cfg *camerascfg.Config) []localCamerasStream {
	streams := make([]localCamerasStream, 0, len(cfg.DeviceAssignments)+len(cfg.RTSPSources))

	for _, assignment := range cfg.DeviceAssignments {
		if assignment.Disabled || !monitoringEnabled(assignment.On) || assignment.OutputPort <= 0 {
			continue
		}
		name := strings.TrimSpace(assignment.Name)
		if name == "" {
			name = strings.TrimSpace(assignment.MatchKey)
		}
		streams = append(streams, localCamerasStream{
			Name:       name,
			ShortID:    strings.TrimSpace(assignment.ShortID),
			OutputPort: assignment.OutputPort,
			Kind:       "USB",
		})
	}

	for _, source := range cfg.RTSPSources {
		if !source.Enabled || !monitoringEnabled(source.On) || source.OutputPort <= 0 {
			continue
		}
		name := strings.TrimSpace(source.Name)
		if name == "" {
			name = strings.TrimSpace(source.SourceID)
		}
		streams = append(streams, localCamerasStream{
			Name:       name,
			ShortID:    strings.TrimSpace(source.ShortID),
			OutputPort: source.OutputPort,
			Kind:       "RTSP",
		})
	}

	sort.Slice(streams, func(i, j int) bool {
		if cmp := compareShortIDs(streams[i].ShortID, streams[j].ShortID); cmp != 0 {
			return cmp < 0
		}
		if streams[i].OutputPort != streams[j].OutputPort {
			return streams[i].OutputPort < streams[j].OutputPort
		}
		leftName := strings.ToLower(strings.TrimSpace(streams[i].Name))
		rightName := strings.ToLower(strings.TrimSpace(streams[j].Name))
		if leftName != rightName {
			return leftName < rightName
		}
		return streams[i].Kind < streams[j].Kind
	})

	return streams
}

func compareShortIDs(left, right string) int {
	left = strings.ToUpper(strings.TrimSpace(left))
	right = strings.ToUpper(strings.TrimSpace(right))

	if left == right {
		return 0
	}
	if left == "" {
		return 1
	}
	if right == "" {
		return -1
	}

	leftPrefix, leftNumber, leftHasNumber := splitShortID(left)
	rightPrefix, rightNumber, rightHasNumber := splitShortID(right)
	if leftPrefix != rightPrefix {
		if leftPrefix < rightPrefix {
			return -1
		}
		return 1
	}
	if leftHasNumber && rightHasNumber && leftNumber != rightNumber {
		if leftNumber < rightNumber {
			return -1
		}
		return 1
	}
	if left < right {
		return -1
	}
	return 1
}

func splitShortID(raw string) (string, int, bool) {
	index := len(raw)
	for i, r := range raw {
		if r >= '0' && r <= '9' {
			index = i
			break
		}
	}
	if index == len(raw) {
		return raw, 0, false
	}
	value, err := strconv.Atoi(raw[index:])
	if err != nil {
		return raw, 0, false
	}
	return raw[:index], value, true
}

func localCaptureAddresses() []string {
	seen := make(map[string]struct{})
	addresses := make([]string, 0, 8)
	add := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		addresses = append(addresses, trimmed)
	}

	add("127.0.0.1")
	add("localhost")
	if hostname, err := os.Hostname(); err == nil {
		add(hostname)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP == nil {
				continue
			}
			if v4 := ipNet.IP.To4(); v4 != nil {
				add(v4.String())
			}
		}
	}

	sort.Strings(addresses)
	return addresses
}

func matchLocalUnicastDestination(destinations []string, localAddresses []string) (string, bool) {
	localSet := make(map[string]struct{}, len(localAddresses))
	for _, address := range localAddresses {
		localSet[strings.ToLower(strings.TrimSpace(address))] = struct{}{}
	}

	for _, destination := range destinations {
		trimmed := strings.TrimSpace(destination)
		if trimmed == "" {
			continue
		}
		if parsed := net.ParseIP(trimmed); parsed != nil && parsed.IsLoopback() {
			return trimmed, true
		}
		if _, ok := localSet[strings.ToLower(trimmed)]; ok {
			return trimmed, true
		}
	}

	return "", false
}

func loadLocalCamerasImportPreview(option localCamerasVersionOption) (*localCamerasImportPreview, error) {
	cfg, err := camerascfg.LoadConfigFromDir(option.ConfigDir)
	if err != nil {
		return nil, err
	}

	ordered := collectLocalCamerasStreams(cfg)
	if len(ordered) == 0 {
		return nil, fmt.Errorf("no enabled local camera streams were found in %s", option.ConfigPath)
	}

	preview := &localCamerasImportPreview{
		Version:        option,
		LocalAddresses: localCaptureAddresses(),
		OrderedStreams: append([]localCamerasStream(nil), ordered...),
	}

	if cfg.Unicast.Enabled {
		preview.Mode = "unicast"
		preview.ListenIP = "0.0.0.0"
		preview.CamerasAddressLabel = "Cameras unicast address"
		preview.ReplaysAddressValue = preview.ListenIP
		for _, destination := range cfg.Unicast.Destinations {
			if !destination.Enabled {
				continue
			}
			trimmed := strings.TrimSpace(destination.Address)
			if trimmed == "" {
				continue
			}
			preview.EnabledDestinations = append(preview.EnabledDestinations, trimmed)
		}
		sort.Strings(preview.EnabledDestinations)
		if matched, ok := matchLocalUnicastDestination(preview.EnabledDestinations, preview.LocalAddresses); ok {
			preview.CompatibilityAllowed = true
			preview.CompatibilityMessage = "Cameras configuration allows capturing the replays."
			preview.CamerasAddressValue = matched
		} else {
			preview.CompatibilityAllowed = false
			preview.CompatibilityMessage = "Cameras configuration does not allow capturing the replays. Cameras is not unicasting to this machine."
			if len(preview.EnabledDestinations) == 0 {
				preview.CamerasAddressValue = "none"
			} else {
				preview.CamerasAddressValue = strings.Join(preview.EnabledDestinations, ", ")
			}
		}
	} else {
		preview.Mode = "multicast"
		preview.ListenIP = strings.TrimSpace(cfg.Multicast.IP)
		if preview.ListenIP == "" {
			preview.ListenIP = "239.255.0.1"
		}
		preview.CompatibilityAllowed = true
		preview.CompatibilityMessage = "Cameras configuration allows capturing the replays."
		preview.CamerasAddressLabel = "Cameras multicast address"
		preview.CamerasAddressValue = preview.ListenIP
		preview.ReplaysAddressValue = preview.ListenIP
	}

	limit := len(ordered)
	if limit > maxImportedCameraStreams {
		limit = maxImportedCameraStreams
	}
	preview.ImportedStreams = append([]localCamerasStream(nil), ordered[:limit]...)
	if limit < len(ordered) {
		preview.AdditionalStreams = append([]localCamerasStream(nil), ordered[limit:]...)
	}

	return preview, nil
}

func formatLocalCamerasImportPreview(preview *localCamerasImportPreview) string {
	if preview == nil {
		return "No preview available."
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Local Cameras Module version: %s\n", preview.Version.Version))
	builder.WriteString(fmt.Sprintf("Config file: %s\n", preview.Version.ConfigPath))
	builder.WriteString(fmt.Sprintf("Read mode: %s\n", strings.Title(preview.Mode)))
	builder.WriteString(fmt.Sprintf("Replays listener IP: %s\n", preview.ListenIP))

	if preview.Mode == "unicast" {
		builder.WriteString("Enabled unicast destinations read from Cameras:\n")
		if len(preview.EnabledDestinations) == 0 {
			builder.WriteString("  (none)\n")
		} else {
			for _, destination := range preview.EnabledDestinations {
				builder.WriteString(fmt.Sprintf("  - %s\n", destination))
			}
		}
	}

	builder.WriteString("\nImported into Replays camera order (sorted by short ID):\n")
	for index, stream := range preview.ImportedStreams {
		builder.WriteString(fmt.Sprintf("  camera%dPort = %d  %s  %s [%s]\n",
			index+1,
			stream.OutputPort,
			formatPreviewShortID(stream.ShortID),
			stream.Name,
			stream.Kind,
		))
	}
	for index := len(preview.ImportedStreams); index < maxImportedCameraStreams; index++ {
		builder.WriteString(fmt.Sprintf("  camera%dPort = 0\n", index+1))
	}

	if len(preview.AdditionalStreams) > 0 {
		builder.WriteString("\nAdditional local Camera streams not imported (Replays supports 4):\n")
		for _, stream := range preview.AdditionalStreams {
			builder.WriteString(fmt.Sprintf("  - %d  %s  %s [%s]\n",
				stream.OutputPort,
				formatPreviewShortID(stream.ShortID),
				stream.Name,
				stream.Kind,
			))
		}
	}

	return builder.String()
}

func formatPreviewShortID(shortID string) string {
	trimmed := strings.TrimSpace(shortID)
	if trimmed == "" {
		return "(no short ID)"
	}
	return trimmed
}

func formatLocalCamerasImportDetails(preview *localCamerasImportPreview) string {
	if preview == nil {
		return "No local Cameras Module configuration loaded."
	}

	var builder strings.Builder
	if !preview.CompatibilityAllowed {
		builder.WriteString(preview.CompatibilityMessage)
		builder.WriteString("\n")
	}
	builder.WriteString(fmt.Sprintf("%s: %s\n", preview.CamerasAddressLabel, preview.CamerasAddressValue))
	builder.WriteString(fmt.Sprintf("Replays listening on: %s", preview.ReplaysAddressValue))

	return builder.String()
}

func formatLocalCamerasStreamLine(index int, stream localCamerasStream) string {
	return fmt.Sprintf("%d. %s  %s [%s]  port %d",
		index+1,
		formatPreviewShortID(stream.ShortID),
		stream.Name,
		stream.Kind,
		stream.OutputPort,
	)
}

func formatSelectedLocalCamerasMapping(selected []localCamerasStream) string {
	if len(selected) == 0 {
		return "No cameras selected for import."
	}

	var builder strings.Builder
	builder.WriteString("Selected Replays capture order:\n")
	for index, stream := range selected {
		builder.WriteString(fmt.Sprintf("  camera%dPort = %d  %s  %s [%s]\n",
			index+1,
			stream.OutputPort,
			formatPreviewShortID(stream.ShortID),
			stream.Name,
			stream.Kind,
		))
	}
	for index := len(selected); index < maxImportedCameraStreams; index++ {
		builder.WriteString(fmt.Sprintf("  camera%dPort = 0\n", index+1))
	}

	return strings.TrimRight(builder.String(), "\n")
}

func applyLocalCamerasImport(cfg *replayscfg.Config, preview *localCamerasImportPreview, selected []localCamerasStream, configFilePath string) error {
	settings := cfg.Multicast
	settings.Enabled = true
	settings.IP = preview.ListenIP
	settings.Camera1Port = 0
	settings.Camera2Port = 0
	settings.Camera3Port = 0
	settings.Camera4Port = 0

	ports := []*int{&settings.Camera1Port, &settings.Camera2Port, &settings.Camera3Port, &settings.Camera4Port}
	for index, stream := range selected {
		if index >= len(ports) {
			break
		}
		*ports[index] = stream.OutputPort
	}

	if err := replayscfg.UpdateMpegTSConfig(configFilePath, settings); err != nil {
		return err
	}

	cfg.Multicast = settings
	return nil
}

func showLocalCamerasImportDialog(cfg *replayscfg.Config, window fyne.Window) {
	options, err := discoverLocalCamerasVersions()
	if err != nil {
		if errors.Is(err, errRemoteCamerasOnly) {
			dialog.ShowInformation("Cameras Module Not Local",
				"No local Cameras Module installation was found.\n\n"+
					"This Replays instance appears to be receiving streams from a Cameras Module running on another machine.\n\n"+
					"Use \"Cameras Module Stream Configuration\" to configure the stream ports.",
				window)
		} else {
			dialog.ShowError(fmt.Errorf("failed to scan local Cameras Module versions: %w", err), window)
		}
		return
	}
	if len(options) == 0 {
		dialog.ShowInformation("Not Available", "No local Cameras Module versions with a config.toml file were found.", window)
		return
	}

	optionByLabel := make(map[string]localCamerasVersionOption, len(options))
	labels := make([]string, 0, len(options))
	for _, option := range options {
		optionByLabel[option.Label] = option
		labels = append(labels, option.Label)
	}

	versionSelect := widget.NewSelect(labels, nil)
	detailsLabel := widget.NewLabel("")
	detailsLabel.Wrapping = fyne.TextWrapWord
	selectionHint := widget.NewLabel("")
	selectionHint.Wrapping = fyne.TextWrapWord
	selectionList := container.NewVBox()
	selectionListScroll := container.NewScroll(selectionList)
	selectionListScroll.SetMinSize(fyne.NewSize(700, 220))

	var currentPreview *localCamerasImportPreview
	var selectionRows []localCamerasSelectionRow
	selectedStreams := func() []localCamerasStream {
		selected := make([]localCamerasStream, 0, len(selectionRows))
		for _, row := range selectionRows {
			if row.check.Checked {
				selected = append(selected, row.stream)
			}
		}
		return selected
	}
	updateSelectionUI := func() {
		selected := selectedStreams()
		checkedCount := len(selected)
		for _, row := range selectionRows {
			if row.check.Checked || checkedCount < maxImportedCameraStreams {
				row.check.Enable()
			} else {
				row.check.Disable()
			}
		}

		hintText := fmt.Sprintf("Enabled cameras are listed in capture order. Check up to %d cameras to import into Replays.", maxImportedCameraStreams)
		if currentPreview != nil {
			if len(currentPreview.OrderedStreams) <= maxImportedCameraStreams {
				hintText += " All enabled cameras are selected by default."
			} else {
				hintText += fmt.Sprintf(" The first %d are selected by default. Uncheck one to select another.", maxImportedCameraStreams)
			}
		}
		selectionHint.SetText(hintText)
	}
	loadSelection := func(label string) {
		option, ok := optionByLabel[label]
		if !ok {
			currentPreview = nil
			selectionRows = nil
			detailsLabel.SetText("No local Cameras Module version selected.")
			selectionHint.SetText("")
			selectionList.Objects = []fyne.CanvasObject{}
			selectionList.Refresh()
			return
		}

		preview, loadErr := loadLocalCamerasImportPreview(option)
		if loadErr != nil {
			currentPreview = nil
			selectionRows = nil
			detailsLabel.SetText(fmt.Sprintf("Failed to read %s\n\n%v", option.ConfigPath, loadErr))
			selectionHint.SetText("")
			selectionList.Objects = []fyne.CanvasObject{}
			selectionList.Refresh()
			return
		}

		currentPreview = preview
		detailsLabel.SetText(formatLocalCamerasImportDetails(preview))

		selectionRows = make([]localCamerasSelectionRow, 0, len(preview.OrderedStreams))
		rowObjects := make([]fyne.CanvasObject, 0, len(preview.OrderedStreams))
		defaultSelectedCount := len(preview.ImportedStreams)
		for index, stream := range preview.OrderedStreams {
			check := widget.NewCheck("", nil)
			check.SetChecked(index < defaultSelectedCount)
			rowLabel := widget.NewLabel(formatLocalCamerasStreamLine(index, stream))
			rowLabel.Wrapping = fyne.TextWrapWord
			selectionRows = append(selectionRows, localCamerasSelectionRow{stream: stream, check: check})
			rowObjects = append(rowObjects, container.NewBorder(nil, nil, check, nil, rowLabel))
		}
		selectionList.Objects = rowObjects
		selectionList.Refresh()
		updateSelectionUI()
		for index := range selectionRows {
			selectionRows[index].check.OnChanged = func(bool) {
				updateSelectionUI()
			}
		}
	}
	versionSelect.OnChanged = loadSelection
	versionSelect.SetSelected(labels[0])

	hint := widget.NewLabel("Pick the local Cameras Module version to read. The newest version is selected by default.")
	hint.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Cameras Module version", versionSelect),
		),
		hint,
		detailsLabel,
		selectionHint,
		selectionListScroll,
	)

	dlg := dialog.NewCustomConfirm("Use Streams from Local Cameras Module", "Use", "Cancel", content,
		func(use bool) {
			if !use {
				return
			}
			if currentPreview == nil {
				dialog.ShowError(fmt.Errorf("no valid local Cameras Module configuration is selected"), window)
				return
			}
			selected := selectedStreams()
			if len(selected) == 0 {
				dialog.ShowError(fmt.Errorf("select at least one enabled camera to import"), window)
				return
			}

			configFilePath := filepath.Join(config.GetInstallDir(), "config.toml")
			if err := applyLocalCamerasImport(cfg, currentPreview, selected, configFilePath); err != nil {
				dialog.ShowError(fmt.Errorf("failed to save imported Cameras Module streams: %w", err), window)
				return
			}

			successDialog := dialog.NewInformation("Success", "Local Cameras Module stream configuration loaded successfully. The application will now exit. Please restart it.", window)
			successDialog.SetOnClosed(func() {
				window.Close()
				os.Exit(0)
			})
			successDialog.Show()
		}, window)
	dlg.Resize(fyne.NewSize(760, 460))
	dlg.Show()
}
