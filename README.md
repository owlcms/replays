# Simple Self-Service Jury Replays for owlcms

This application records jury replay videos automatically, using the clock and decision information sent by owlcms.  The jury can immediately watch the replays using a web browser.  The replays are trimmed to remove idle time before the actual lift.

The program was initially meant for regional, national or multi-national events that use a single replay camera per platform, but now supports multiple cameras.

As an additional benefit, this creates a full video archive of all the lifts in the competition, organized by sessions, correctly labelled with the athlete, time of day, lift type and attempt number.

## Supported platforms

> Note: this version requires owlcms version **56**.0.0-beta or newer.  Use "Install Additional Versions" and "Show Prereleases" to get it.

- Raspberry Pi 5 with SSD or Raspberry Pi 500 with a large-enough SD card/an external SSD
- Windows 10/11 on a recent laptop (a good GPU will be required for multiple cameras)

See the [Equipment Setup](#equipment-setup) notes at the bottom of this page.

## Overview

- The replay app makes the replay video available on a web page that updates automatically as videos become available. Videos are listed with the most recent at the top.
  - Clicking on a link opens a tab where the video can be seen, paused and restarted at will.
  - The jury simply switches tabs to go back to the jury console or jury scoreboard
  
![image](https://github.com/user-attachments/assets/648d8679-2501-4c17-9c5f-1ab20a9a8509)

  
## Installation and Use

Detailed instructions can be found at

https://owlcms.github.io/owlcms4-prerelease/#/JuryReplays


## Equipment Setup

The replay application is designed to be usable on the jury laptop.  Typical setups:

- A dedicated good quality USB webcam is connected using an Active USB cable (to account for the distance)
- A regular camera with HDMI output is connected using a HDMI-to-USB capture adapter.  If this camera is also used for streaming, then a splitter or a pass-through adapter is used.
- Professional SDI feeds are used from the cameras.  The SDI to USB conversion would take place in the video control room, and the jury would access the replays using a browser.  Multiple instances of the application would run on a a single computer if needed.

