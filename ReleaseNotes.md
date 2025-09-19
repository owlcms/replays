**Change log**

- 1.8.1  Make sure the default config file is the latest one.
- 1.8.0  Changes in configuration file.  Ffmpeg requires a specific ordering of parameters, and this forces a split between inputParameters (how to read the camera) and outputParameters (how to format the output).  The previous params is treated as outputParameters.
    - See [the new default.toml file](https://github.com/owlcms/replays/blob/main/internal/config/default.toml) to adjust your configuration

- 1.7.2 Configuration file errors are now shown on startup
- 1.7.1 Added logging option `logFfmpeg = true` to enable detailed logging of ffmpeg output for each invocation
- 1.7.1 Fixed the ordering of the arguments to ffmpeg to ensure that the size parameter applied to the recording as opposed to resizing.
- 1.7.0 Fixed the processing when there is a space in the user name on Windows
- 1.7.0 Fixed the camera lookup code to correctly use the locally copied ffmpeg on Windows
- 1.7.0 Added code to correctly close ports on shutdown
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
