#!/bin/bash
# Install Jellyfin's ffmpeg build with NVENC support
# This replaces Ubuntu's ffmpeg which lacks NVIDIA hardware encoding
#
# Run as: sudo ./install-ffmpeg-nvenc.sh

set -e

FFMPEG_URL="https://repo.jellyfin.org/files/ffmpeg/ubuntu/latest-7.x/amd64"

# Detect Ubuntu version
if [ -f /etc/os-release ]; then
    . /etc/os-release
    UBUNTU_CODENAME="${VERSION_CODENAME:-noble}"
else
    UBUNTU_CODENAME="noble"
fi

echo "=== Jellyfin FFmpeg Installer (with NVENC) ==="
echo "Detected Ubuntu codename: $UBUNTU_CODENAME"
echo

# Check for root
if [ "$EUID" -ne 0 ]; then
    echo "Please run as root: sudo $0"
    exit 1
fi

# Step 1: Remove ubuntuhandbook1 PPA if present
echo "Checking for ubuntuhandbook1 ffmpeg PPA..."
if grep -rq "ubuntuhandbook1/ffmpeg" /etc/apt/sources.list.d/ 2>/dev/null; then
    echo "Removing ubuntuhandbook1 ffmpeg PPA..."
    add-apt-repository -y --remove ppa:ubuntuhandbook1/ffmpeg7 2>/dev/null || true
    add-apt-repository -y --remove ppa:ubuntuhandbook1/ffmpeg6 2>/dev/null || true
    apt-get update
fi

# Step 2: Remove existing ffmpeg packages
echo "Removing existing ffmpeg packages..."
apt-get remove -y ffmpeg libavcodec-extra libavcodec60 libavformat60 libavutil58 2>/dev/null || true
apt-get autoremove -y

# Step 3: Download Jellyfin ffmpeg
echo "Downloading Jellyfin ffmpeg 7.x for $UBUNTU_CODENAME..."
TMPDIR=$(mktemp -d)
cd "$TMPDIR"

# Use noble (24.04) or jammy (22.04) based on detected version
case "$UBUNTU_CODENAME" in
    noble|oracular|plucky)
        DEB_NAME="jellyfin-ffmpeg7_7.1.3-3-noble_amd64.deb"
        ;;
    jammy|kinetic|lunar|mantic)
        DEB_NAME="jellyfin-ffmpeg7_7.1.3-3-jammy_amd64.deb"
        ;;
    *)
        echo "Warning: Unknown Ubuntu version, using noble package"
        DEB_NAME="jellyfin-ffmpeg7_7.1.3-3-noble_amd64.deb"
        ;;
esac

wget -q --show-progress "${FFMPEG_URL}/${DEB_NAME}"

# Step 4: Install Jellyfin ffmpeg
echo "Installing Jellyfin ffmpeg..."
dpkg -i "$DEB_NAME" || apt-get -f install -y

# Step 5: Create symlinks to make it the default ffmpeg tools
echo "Creating symlinks..."
JELLYFIN_PATH="/usr/lib/jellyfin-ffmpeg"

for bin in ffmpeg ffprobe ffplay; do
    if [ -f "$JELLYFIN_PATH/$bin" ]; then
        # Backup existing if it's a real file (not symlink)
        if [ -f "/usr/bin/$bin" ] && [ ! -L "/usr/bin/$bin" ]; then
            mv "/usr/bin/$bin" "/usr/bin/${bin}.ubuntu-backup"
        fi
        ln -sf "$JELLYFIN_PATH/$bin" "/usr/bin/$bin"
        echo "  /usr/bin/$bin -> $JELLYFIN_PATH/$bin"
    fi
done

# Cleanup
rm -rf "$TMPDIR"

# Step 6: Verify installation
echo
echo "=== Verification ==="
echo "FFmpeg version:"
ffmpeg -version 2>&1 | head -1
echo "FFplay version:"
ffplay -version 2>&1 | head -1
echo
echo "NVENC support:"
if ffmpeg -hide_banner -encoders 2>&1 | grep -q h264_nvenc; then
    echo "  ✓ h264_nvenc available"
else
    echo "  ✗ h264_nvenc NOT available (check NVIDIA drivers)"
fi
echo
echo "VAAPI support:"
if ffmpeg -hide_banner -encoders 2>&1 | grep -q h264_vaapi; then
    echo "  ✓ h264_vaapi available"
else
    echo "  ✗ h264_vaapi NOT available"
fi
echo
echo "QSV support:"
if ffmpeg -hide_banner -encoders 2>&1 | grep -q h264_qsv; then
    echo "  ✓ h264_qsv available"
else
    echo "  ✗ h264_qsv NOT available"
fi

echo
echo "=== Installation complete ==="

