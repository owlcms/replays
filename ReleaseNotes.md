**Change log**

- 1.6.0 Additional endpoint for replays:
    - The URL .../replay/1.mp4 will give back the latest replay from camera1 (and so on for other cameras). Recording stops 2 seconds after decision is visible, allow 1 second more for trimming.
    - Use HDMI splitter(s) to feed OBS and the replay system. For webcams, OBS and replays each need their own.
- 1.5.0 ability to sort the videos per athlete then time to make it easier to find replays after a session.
- 1.4.0 Menu Entry to list cameras: Help > List Cameras
- 1.4.0 Fixes for clock start/restart
- 1.4.0 On Windows, ffmpeg is downloaded and the local copy is used, no install is necessary.
- 1.4.0 Fixes for platform names with spaces


**Instructions**

For installation and usage instructions, see the main [README.md](https://github.com/owlcms/replays/blob/main/README.md) 

**Supported platforms:**

- for Raspberry Pi : use the `replays` program
- for Windows: use `replays.exe`
