**Change log**

> This version requires owlcms 56.0.0-beta or newer (use "additional versions" "show prereleases" to get)

- 1.2.0 Fixes for the initial plaftform detection
- 1.2.0 Logging to the logs/replays.log now works again
- 1.1.0 Replays are organized by session; it possible to see replays from a prior session.
- 1.1.0 Multiple cameras are supported
- 1.1.0 replays gets the list of platforms from owlcms and allows selecting which one
- 1.0.1: new ffmpegParams entry for additional parameters (loglevel, codec, etc.)
- 1.0.0 replays now interacts with owlcms using MQTT
  - At startup, if the server location is not set or incorrect, replays will scan the local 192.168.x network to find owlcms
  - A dialog allows setting th address if the scan fails


**Instructions**

For installation and usage instructions, see the main [README.md](https://github.com/owlcms/replays/blob/main/README.md) 

**Supported platforms:**

- for Raspberry Pi : use the `replays` program
- for Windows: use `replays.exe`
