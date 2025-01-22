## Go Jury Replay Capture for owlcms

This project aims to capture jury replay videos as instructed by the owlcms software.
The program listens to events pushed over http using the same json contents as used for the publicresults and wise-eyes modules.
ffmpeg is used to capture the videos.

It is expected that owlcms will get back the videos using sftp.  As basic web page may be provided for when that is not in use (TBD)

The target platforms are the Raspberry Pi with v4l2 and Windows laptops with gdigrab.
