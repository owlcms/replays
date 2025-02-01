**Change log**

> This version requires owlcms 55.3.0-beta or newer (use "additional versions" "show prereleases" to get)

- 1.0.1: new ffmpegParams entry for additional parameters (loglevel, codec, etc.)

- replays now interacts with owlcms using MQTT
  - No longer necessary to configure owlcms
  - At startup, if the server location is not set or wrong, replays will scan the local 192.168.x network to find owlcms
  - If the local area network is not a 192.168.x network, of if the scan is done when owlcms is not running, a dialog
    allows setting the address
- replays gets the list of platforms from owlcms and allows selecting which one

**Instructions**

For installation and usage instructions, see the main [README.md](https://github.com/owlcms/replays/blob/main/README.md) 

**Supported platforms:**

- for Raspberry Pi : use the `replays` program
- for Windows: use `replays.exe`
