# OBS, NVIDIA Pascal, and HWE Kernel Baseline

Use this runbook for an Ubuntu 24.04 event machine with a Pascal NVIDIA GPU that
must run OBS 32.2 and encode with NVENC.

## Verified Target

| Component | Target |
| --- | --- |
| Ubuntu kernel track | Ubuntu 24.04 HWE (`linux-image-generic-hwe-24.04`) |
| Verified kernel | `7.0.0-28-generic` |
| NVIDIA GPU | Pascal, verified with GTX 1050 Ti / GP107M |
| NVIDIA driver | `580.173.02-0ubuntu0.24.04.1` |
| Driver module installation | `nvidia-dkms-580`, built for the booted kernel |
| OBS Studio | `32.2.0-0obsproject1~noble` from `ppa:obsproject/obs-studio` |
| OBS video encoder | `NVIDIA NVENC H.264` |

Do not use NVIDIA's standalone `.run` installer. It bypasses Ubuntu package
management and makes kernel updates and driver recovery harder.

NVIDIA's current general production branch does not support Pascal. The `580`
branch supports Pascal; `580.173.02` is the verified Ubuntu-packaged release
for this baseline. The NVIDIA kernel-driver version does not need to resemble
the Linux kernel version: DKMS builds the driver module for the booted kernel.

## Install or Repair

Run these commands on the target Ubuntu machine. Do not add the Graphics
Drivers PPA for this baseline.

```bash
sudo rm -f /etc/apt/preferences.d/nvidia-580-142.pref
sudo apt-get update
sudo apt-get install -y \
  linux-image-generic-hwe-24.04 \
  linux-headers-generic-hwe-24.04 \
  nvidia-driver-580=580.173.02-0ubuntu0.24.04.1
sudo update-grub
sudo reboot
```

If the exact NVIDIA package version is no longer available in Ubuntu's active
repositories, stop before installing a different version. Confirm that the
candidate is a Pascal-supporting `580` package and that it can be installed with
the matching `nvidia-driver-580` metapackage.

If `apt-get update` fails because an OBS snapshot source lacks a Release file,
disable that source. Do not use a snapshot URL for the OBS PPA:

```bash
sudo grep -R "snapshot.ppa.launchpadcontent.net/obsproject" /etc/apt/sources.list /etc/apt/sources.list.d 2>/dev/null
```

## Validate NVIDIA and NVENC

After reboot, all of the following must succeed before OBS is configured:

```bash
uname -r
dkms status
modinfo -F version nvidia
nvidia-smi
ffmpeg -hide_banner -f lavfi -i testsrc2=size=128x72:rate=1 \
  -frames:v 1 -c:v h264_nvenc -f null -
```

Expected results:

- `uname -r` reports the intended HWE kernel.
- `dkms status` reports NVIDIA `580.173.02` installed for that kernel.
- `modinfo -F version nvidia` and `nvidia-smi` report `580.173.02`.
- The FFmpeg test completes and prints `h264_nvenc`; it must not report
  `CUDA_ERROR_NO_DEVICE`.

## Install OBS 32.2

Use the official OBS PPA, then install the required version:

```bash
sudo add-apt-repository -y ppa:obsproject/obs-studio
sudo apt-get update
sudo apt-get install -y obs-studio=32.2.0-0obsproject1~noble
obs --version
```

In OBS, open `Settings > Output` and choose `NVIDIA NVENC H.264` for streaming
and recording. Use the Advanced output mode when separate settings are needed.

## YouTube Sessions

For scheduled YouTube sessions that each have their own stream key:

1. Create one OBS profile per scheduled session.
2. Set `Settings > Stream > Service` to `YouTube - RTMPS`.
3. Select `Use Stream Key` and enter only the key for that session.
4. Keep stream keys out of files, scripts, scene names, and screenshots.

Use a separate `YouTube Test` profile with an Unlisted YouTube test stream.
Profiles retain stream and output configuration; create a separate scene
collection only when the source layout differs.

## Agent Handoff

Give an agent this prompt on a replacement or repaired machine:

```text
Use replays/docs/obs-nvidia-pascal-baseline.md as the required baseline.
Set up Ubuntu 24.04 HWE, an Ubuntu-packaged Pascal-compatible NVIDIA 580 driver
through nvidia-driver-580 and DKMS, OBS 32.2 from the official OBS PPA, and
verify real h264_nvenc encoding with FFmpeg. Do not use NVIDIA .run installers,
the Graphics Drivers PPA, a broken OBS snapshot source, or stream keys. Stop
before substituting a driver version if the documented package is unavailable.
```