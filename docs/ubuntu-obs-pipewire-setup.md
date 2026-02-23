# Ubuntu 24.04 Dual Boot + OBS PipeWire Audio Capture

## Part 1 — Ubuntu 24.04 Install (Dual Boot with Windows)

### Prerequisites
- USB stick with Ubuntu 24.04 ISO
- 40GB+ unallocated space on your disk (already done via diskpart)
- Windows booting correctly

### Boot from USB
1. Plug in USB stick
2. Power on, spam **F12** for boot menu
3. Select the USB stick
4. Choose **"Try or Install Ubuntu"**

### Installer Options — Choose Carefully

| Screen | Choice |
|--------|--------|
| Language | English (or your preference) |
| Keyboard | Match your keyboard layout |
| Type of install | **Interactive installation** |
| Applications | **Default selection** |
| Optimise your computer | ✅ Check **"Install third-party software"** (important for Nvidia) |
| Installation type | **"Install Ubuntu alongside Windows Boot Manager"** |
| Disk allocation | Drag divider or accept the 40GB unallocated space |
| Time zone | Your location |
| User account | Set your username and password |

> ⚠️ Do NOT choose "Erase disk" — that will wipe Windows.  
> ⚠️ Do NOT manually partition unless "alongside Windows" option is missing.

### After Install
- Remove USB stick when prompted
- Reboot — you should see a Grub menu with **Ubuntu** and **Windows** options

---

## Part 2 — Desktop Customization

### Center Taskbar Icons and Move Show Apps to Left



**Built-in (no extensions needed):**
1. Open **Settings → Appearance → Dock**
2. Set icon position to **Centered**

**For full control including Show Apps button position:**
```bash
sudo apt install gnome-tweaks gnome-shell-extension-manager
```
1. Open **Extension Manager**
2. Search for **"Dash to Dock"**
3. Install it
4. Click the settings gear
5. Set icons to centered and Show Apps button to left

---

## Part 3 — Post-Install Setup

### Install FFmpeg 7
Ubuntu 24.04 ships with FFmpeg 6 by default. FFmpeg 7 improves encoding performance and codec support:

```bash
sudo add-apt-repository ppa:ubuntuhandbook1/ffmpeg7
sudo apt update
sudo apt install ffmpeg
```

Verify:
```bash
ffmpeg -version
```

> ⚠️ FFmpeg is a core system library. If any dependency issues arise after installing, remove the PPA and reinstall the default version:
> ```bash
> sudo apt install ppa-purge
> sudo ppa-purge ppa:ubuntuhandbook1/ffmpeg7
> ```

### Verify PipeWire is Running
```bash
# Install pactl if not present
sudo apt install pulseaudio-utils

pactl info | grep "Server Name"
# Must show: PulseAudio (on PipeWire)
```

### Check Nvidia Driver Version
```bash
nvidia-smi | grep "Driver Version"
```

Ubuntu 24.04 installs driver 535 by default. Upgrade to 550 or newer (580 recommended):

**Easiest method — GUI:**
1. Open **Software & Updates**
2. Click **Additional Drivers** tab
3. Select **nvidia-driver-580** (or highest available)
4. Click **Apply Changes**
5. Reboot

**Terminal method:**
```bash
ubuntu-drivers list
sudo apt install nvidia-driver-580
sudo reboot
```

Verify after reboot:
```bash
nvidia-smi | grep "Driver Version"
```

---

## Part 4 — OBS Studio Install

### Install OBS from Official PPA
```bash
sudo add-apt-repository ppa:obsproject/obs-studio
sudo apt update
sudo apt install obs-studio
```

### Verify OBS Plugins Directory
```bash
ls /usr/lib/x86_64-linux-gnu/obs-plugins/
```
Look for `linux-pipewire.so` — if it's there, skip Part 4 and test directly.

---

## Part 5 — PipeWire Audio Capture Plugin (if needed)

If OBS does not show **"Application Audio Capture (PipeWire)"** as a source:

### Download the Plugin
Go to: https://github.com/dimtpap/obs-pipewire-audio-capture/releases

Download the latest `linux-pipewire-audio-x.x.x.tar.gz`

### Install the Plugin
```bash
mkdir -p ~/.config/obs-studio/plugins
tar -xf linux-pipewire-audio-*.tar.gz -C ~/.config/obs-studio/plugins/
```

Restart OBS.

---

## Part 6 — Configure OBS Audio Capture

1. Open OBS
2. Under **Sources** click **+**
3. Select **"Application Audio Capture (PipeWire)"**
4. Name it (e.g. "Browser Audio")
5. Click OK
6. In the properties dropdown, select your browser (Firefox, Chrome, etc.)
7. Click OK

> 🎵 Play audio on Audiio.com in your browser — you should see the audio meter move in OBS.

---

## Troubleshooting

### OBS source still not showing
Try the Flatpak version of OBS which bundles everything:
```bash
sudo apt install flatpak
flatpak remote-add --if-not-exists flathub https://flathub.org/repo/flathub.flatpakrepo
flatpak install flathub com.obsproject.Studio
flatpak run com.obsproject.Studio
```

### PipeWire not running
```bash
systemctl --user enable --now pipewire pipewire-pulse wireplumber
```

### Wayland (last resort — only if all above fails)
If application audio capture still doesn't work, switch to Wayland session:
```bash
sudo nano /etc/gdm3/custom.conf
# Set: WaylandEnable=true

sudo ln -sf /dev/null /etc/udev/rules.d/61-gdm.rules
sudo reboot
```
At login screen → gear icon → select **"Ubuntu (Wayland)"**

Then verify:
```bash
echo $XDG_SESSION_TYPE   # should say: wayland
```

---

## Summary — In Order of Complexity

1. ✅ Install OBS from PPA → check for PipeWire source
2. ✅ If missing → install `obs-pipewire-audio-capture` plugin from GitHub
3. ✅ If still missing → use Flatpak OBS
4. ✅ Last resort → upgrade to Nvidia 550 + switch to Wayland
