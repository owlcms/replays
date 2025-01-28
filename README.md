# Simple Jury Replays for owlcms

This project aims to capture jury replay videos as instructed by the owlcms software.

This version is targeted at national/regional/multi-national events where a single replay camera is accepted.

The program listens to events pushed over http using the same json contents as used for the publicresults and wise-eyes modules.
ffmpeg is used to capture the videos.

**Supported platforms:**

- for Raspberry Pi : use the `replays` program
- for Windows: use `replays.exe`

**Raspberry Pi instructions:**

- no configuration required.  your video camera should be automatically detected as /dev/video0

**Windows instructions:**

- The ffmpeg program is used for the actual recording, and is a prerequisite.

  - The simplest way to install is to use the command line and type 

    ```
    winget install ffmpeg
    ```

- You need to edit the config.toml file to put the name of your camera

  - To get the name, use the command

    ```
    ffmpeg -f dshow -list_devices true -i dummy
    ```

  - In the configuration file, if your device is listed as `Logitech Webcam C930e` you must add `video=` before the name and use

    ```
    ffmpegCamera = 'video=Logitech Webcam C930e'
    ```


