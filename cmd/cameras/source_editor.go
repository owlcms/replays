package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	camerascfg "github.com/owlcms/replays/internal/config/cameras"
)

const (
	usbIdentityWidth = 320
	usbNameWidth     = 220
	usbShortIDWidth  = 90
	usbPortWidth     = 90
	usbProbeWidth    = 80
	usbSaveWidth     = 80

	rtspEnabledWidth   = 100
	rtspNameWidth      = 180
	rtspShortIDWidth   = 90
	rtspURLWidth       = 430
	rtspPortWidth      = 90
	rtspTransportWidth = 100
	rtspSaveWidth      = 80
	rtspProbeWidth     = 80
	rtspRemoveWidth    = 80
)

func fixedWidth(width float32, obj fyne.CanvasObject) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(width, obj.MinSize().Height), obj)
}

type usbSourceRow struct {
	matchKey     string
	identity     string
	dirty        bool
	saveBtn      *widget.Button
	nameEntry    *widget.Entry
	shortIDEntry *widget.Entry
	portEntry    *widget.Entry
}

func newUSBSourceRow(spec sourceSpec) *usbSourceRow {
	nameEntry := widget.NewEntry()
	nameEntry.SetText(spec.Name)
	shortIDEntry := widget.NewEntry()
	shortIDEntry.SetText(spec.ShortID)
	portEntry := widget.NewEntry()
	if spec.OutputPort > 0 {
		portEntry.SetText(strconv.Itoa(spec.OutputPort))
	}
	saveBtn := widget.NewButton("Apply", nil)
	r := &usbSourceRow{
		matchKey:     spec.Key,
		identity:     spec.Summary,
		saveBtn:      saveBtn,
		nameEntry:    nameEntry,
		shortIDEntry: shortIDEntry,
		portEntry:    portEntry,
	}
	markDirty := func(_ string) { r.markDirty() }
	nameEntry.OnChanged = markDirty
	shortIDEntry.OnChanged = markDirty
	portEntry.OnChanged = markDirty
	return r
}

func (r *usbSourceRow) markDirty() {
	r.dirty = true
	if r.saveBtn != nil {
		r.saveBtn.Importance = widget.HighImportance
		r.saveBtn.Refresh()
	}
}

func (r *usbSourceRow) object(probe, save func()) fyne.CanvasObject {
	r.saveBtn.OnTapped = func() {
		if save != nil {
			save()
		}
		r.dirty = false
		r.saveBtn.Importance = widget.MediumImportance
		r.saveBtn.Refresh()
	}
	probeBtn := widget.NewButton("Probe", func() {
		if probe != nil {
			probe()
		}
	})
	identity := widget.NewLabel(r.identity)
	identity.Truncation = fyne.TextTruncateEllipsis
	return container.NewHBox(
		fixedWidth(usbIdentityWidth, identity),
		fixedWidth(usbNameWidth, r.nameEntry),
		fixedWidth(usbShortIDWidth, r.shortIDEntry),
		fixedWidth(usbPortWidth, r.portEntry),
		widget.NewLabel("USB"),
		fixedWidth(usbProbeWidth, probeBtn),
		fixedWidth(usbSaveWidth, r.saveBtn),
	)
}

func (r *usbSourceRow) assignment() (camerascfg.DeviceAssignment, error) {
	port, err := parseOptionalPort(r.portEntry.Text)
	if err != nil {
		return camerascfg.DeviceAssignment{}, err
	}
	return camerascfg.DeviceAssignment{
		MatchKey:   r.matchKey,
		Name:       strings.TrimSpace(r.nameEntry.Text),
		ShortID:    strings.TrimSpace(r.shortIDEntry.Text),
		OutputPort: port,
	}, nil
}

type rtspSourceRow struct {
	sourceID        string
	isAddRow        bool
	dirty           bool
	saveBtn         *widget.Button
	probeStatus     *widget.Label
	enabledCheck    *widget.Check
	nameEntry       *widget.Entry
	shortIDEntry    *widget.Entry
	urlEntry        *widget.Entry
	portEntry       *widget.Entry
	transportSelect *widget.Select
	detectedCodec   string
}

func newRTSPSourceRow(spec sourceSpec) *rtspSourceRow {
	enabledCheck := widget.NewCheck("Enabled", nil)
	enabledCheck.SetChecked(spec.Enabled)
	nameEntry := widget.NewEntry()
	nameEntry.SetText(spec.Name)
	shortIDEntry := widget.NewEntry()
	shortIDEntry.SetText(spec.ShortID)
	urlEntry := widget.NewEntry()
	urlEntry.SetText(spec.RTSP.RTSPURL)
	portEntry := widget.NewEntry()
	if spec.OutputPort > 0 {
		portEntry.SetText(strconv.Itoa(spec.OutputPort))
	}
	transportSelect := widget.NewSelect([]string{"tcp", "udp", "auto"}, nil)
	transport := strings.ToLower(strings.TrimSpace(spec.Transport))
	if transport == "" {
		transport = "auto"
	}
	transportSelect.SetSelected(transport)

	saveBtn := widget.NewButton("Apply", nil)
	probeStatus := widget.NewLabel("")

	row := &rtspSourceRow{
		sourceID:        spec.Key,
		saveBtn:         saveBtn,
		probeStatus:     probeStatus,
		enabledCheck:    enabledCheck,
		nameEntry:       nameEntry,
		shortIDEntry:    shortIDEntry,
		urlEntry:        urlEntry,
		portEntry:       portEntry,
		transportSelect: transportSelect,
		detectedCodec:   spec.RTSP.Codec,
	}
	row.installAutoEnable()
	return row
}

func newBlankRTSPSourceRow() *rtspSourceRow {
	row := newRTSPSourceRow(sourceSpec{Enabled: false, Transport: "tcp"})
	row.isAddRow = true
	row.enabledCheck.SetChecked(false)
	return row
}

func (r *rtspSourceRow) object(add func(), save func(), probe func(), remove func()) fyne.CanvasObject {
	if r.isAddRow {
		addButton := widget.NewButton("Add", func() {
			if add != nil {
				add()
			}
		})
		return container.NewHBox(
			fixedWidth(rtspEnabledWidth, r.enabledCheck),
			fixedWidth(rtspNameWidth, r.nameEntry),
			fixedWidth(rtspShortIDWidth, r.shortIDEntry),
			fixedWidth(rtspURLWidth, r.urlEntry),
			fixedWidth(rtspPortWidth, r.portEntry),
			fixedWidth(rtspTransportWidth, r.transportSelect),
			fixedWidth(rtspSaveWidth, addButton),
		)
	}

	r.saveBtn.OnTapped = func() {
		if save != nil {
			save()
		}
		r.dirty = false
		r.saveBtn.Importance = widget.MediumImportance
		r.saveBtn.Refresh()
	}
	probeButton := widget.NewButton("Probe", func() {
		if probe != nil {
			probe()
		}
	})
	removeButton := widget.NewButton("Remove", func() {
		if remove != nil {
			remove()
		}
	})
	return container.NewHBox(
		fixedWidth(rtspEnabledWidth, r.enabledCheck),
		fixedWidth(rtspNameWidth, r.nameEntry),
		fixedWidth(rtspShortIDWidth, r.shortIDEntry),
		fixedWidth(rtspURLWidth, r.urlEntry),
		fixedWidth(rtspPortWidth, r.portEntry),
		fixedWidth(rtspTransportWidth, r.transportSelect),
		fixedWidth(rtspSaveWidth, r.saveBtn),
		fixedWidth(rtspProbeWidth, probeButton),
		fixedWidth(rtspRemoveWidth, removeButton),
	)
}

func (r *rtspSourceRow) markDirty() {
	if r.isAddRow {
		return
	}
	r.dirty = true
	if r.saveBtn != nil {
		r.saveBtn.Importance = widget.HighImportance
		r.saveBtn.Refresh()
	}
}

func (r *rtspSourceRow) installAutoEnable() {
	autoEnable := func(_ string) {
		if !r.hasContent() {
			return
		}
		if !r.enabledCheck.Checked {
			r.enabledCheck.SetChecked(true)
		}
		r.markDirty()
	}
	r.nameEntry.OnChanged = autoEnable
	r.shortIDEntry.OnChanged = autoEnable
	r.urlEntry.OnChanged = autoEnable
	r.portEntry.OnChanged = autoEnable
	r.transportSelect.OnChanged = func(_ string) { r.markDirty() }
	r.enabledCheck.OnChanged = func(_ bool) { r.markDirty() }
}

func (r *rtspSourceRow) hasContent() bool {
	return strings.TrimSpace(r.nameEntry.Text) != "" ||
		strings.TrimSpace(r.shortIDEntry.Text) != "" ||
		strings.TrimSpace(r.urlEntry.Text) != "" ||
		strings.TrimSpace(r.portEntry.Text) != ""
}

func (r *rtspSourceRow) source() (camerascfg.RTSPSource, bool, error) {
	urlValue := strings.TrimSpace(r.urlEntry.Text)
	nameValue := strings.TrimSpace(r.nameEntry.Text)
	if urlValue == "" && nameValue == "" {
		return camerascfg.RTSPSource{}, true, nil
	}
	if urlValue == "" {
		return camerascfg.RTSPSource{}, false, fmt.Errorf("RTSP URL is required for configured sources")
	}
	port, err := parseOptionalPort(r.portEntry.Text)
	if err != nil {
		return camerascfg.RTSPSource{}, false, err
	}
	transport := strings.ToLower(strings.TrimSpace(r.transportSelect.Selected))
	if transport == "" {
		transport = "tcp"
	}
	sourceID := strings.TrimSpace(r.sourceID)
	if sourceID == "" {
		sourceID = tempRTSPSourceID(urlValue)
	}
	return camerascfg.RTSPSource{
		SourceID:   sourceID,
		Name:       nameValue,
		ShortID:    strings.TrimSpace(r.shortIDEntry.Text),
		Enabled:    r.enabledCheck.Checked,
		RTSPURL:    urlValue,
		OutputPort: port,
		Transport:  transport,
		Codec:      r.detectedCodec,
	}, false, nil
}

func tempRTSPSourceID(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err == nil {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		raw = parsed.String()
	}
	hash := sha1.Sum([]byte(strings.TrimSpace(raw)))
	return "rtsp-" + hex.EncodeToString(hash[:])[:10]
}

func parseOptionalPort(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", raw)
	}
	return port, nil
}