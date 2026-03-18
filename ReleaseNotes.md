**Change log**

- 2.1.0: cameras: Added support for RTSP feeds
- 2.1.0: cameras: Restructured the user interface

- 2.0.0: replays and cameras are now installed and started from the control panel
  - preferred resolutions and fps and GPU settings are stored in a shared ffmpeg.toml config file
  - a shared copy of ffmpeg 7.1 is downloaded in the control panel installation directory
- 2.0.0: replays will now, by default, use the H.264 streams created by the cameras program
  - *with normal consumer-grade switches and routers you want to use unicast mode in your cameras config file but this requires knowing your OBS destination addresses*
  - from the control panel, start the cameras program to autodetect your cameras
  - check which port is camera 1 etc.
  - if you have cameras attached to several machines, start the cameras on each, and adjust the ports so they don't conflict
  - start replays, and adjust the cameras if needed (change the ports so camera1 is the correct one, etc.)
  - If you want the old behavior, tell replays to stop using multicast
- 2.0.0: The cameras module uses MJPEG instead of H.264 because dshow cannot recover from startup sync issues.
  - Linux v4l2 has no issues.  Windows laptop needs a good GPU to process.
- 2.0.0: The cameras module can do both multicasting/unicasting
  - When using customer-level switches the traffic from the video propagates up to the main network
  - Use point-to-point unicasting if you only have one or two OBS listening, the overhead is minimal
  - added wallclock timestamps to better support unicasting vs tee mux

- 1.9.0 added a new `cameras` (`cameras.exe` on windows) to start a multicast UDP H.264 stream for each of the detected cameras
  - use `-includeraw` to include the laptop built-in cameras during testing.
- 1.9.0 Add Auto-detection of cameras: an auto.toml file is written that can be edited, and has priority over config.toml.


**Instructions**

For installation and usage instructions, see the main [README.md](https://github.com/owlcms/replays/blob/main/README.md) 

**Supported platforms:**

- for Raspberry Pi : use the `replays` program
- for Windows: use `replays.exe`
