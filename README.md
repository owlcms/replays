# Self-service Jury Replays for owlcms

This project aims to capture jury replay videos as instructed by the owlcms software.
The jury can, on their own, without external intervention, watch the replays using a web browser.

As an additional benefit, this creates a full video archive of all the lifts in the competition, correctly labeled with the athlete,  attempt number and time of day.

This version is targeted at national/regional/multi-national events that use a single replay camera.
Future versions may eventually support multiple cameras, but no commitment is made.

## Supported platforms

Download the program from the [release area](https://github.com/owlcms/replays/releases)

- for Raspberry Pi : use the `replays` program
- for Windows: use `replays.exe`

## How to use
(after doing the setup steps shown down this page)

- Start the replay application
  
  ![replays40](https://github.com/user-attachments/assets/ac498325-30a4-4d97-8195-7e02fab7bf06)

- Start a session in owlcms
  - owlcms will automatically send timer and decision information to the replays app

    ![replays41](https://github.com/user-attachments/assets/42c8e2eb-17e7-4cd7-90d3-9528d3126b3f)

  - The replays app will start recording when the clock starts, and stop once the decision has been shown
    
    ![replays50](https://github.com/user-attachments/assets/79201b88-701e-4884-a4d2-2f64b5ffcd5d)

  - After the decision is shown, the app trims the video down to remove the wait time before the actual lift (when the clock was last stopped).

    ![replay60](https://github.com/user-attachments/assets/4090f9ba-7671-41a8-95ba-07f30496944c)
 
  - The video is shown as being available

    ![image](https://github.com/user-attachments/assets/0e15e9d0-2b7a-49f8-bd21-66307c4f1437)

  - The replay app makes the video available on a web page that updates automatically as videos become available. Videos are listed with the most recent at the top.
    
    ![image](https://github.com/user-attachments/assets/bd8192ba-7e1d-46d3-a893-ec3a3e1f9d09)

## owlcms Setup instructions

- Start the replay program and open the `Help` > `owlcms Configuration Settings` menu
- In owlcms, set the `Language and System Settings` > `Connections` Video URL option to that value
![replays10](https://github.com/user-attachments/assets/7c8590b0-b477-4c12-bea3-925386d8e40a)

## Raspberry Pi Initial Setup Instructions

- Right-click on the program.  Set the execution permissions to "anyone"
- No further actions required.  By default your camera will be on /dev/video0. 

## Windows Initial Setup Instructions

- The ffmpeg program is used for the actual recording, and is a prerequisite.

  - The simplest way to install is to use the command line and type 

    ```
    winget install ffmpeg
    ```

- You need to edit the config.toml file to put the name of your camera

  - To get the name, use the command

    ```
    ffmpeg -hide-banner -f dshow -list_devices true -i dummy
    ```

  - Open the configuration file.  Start the replay.exe program and use the Files menu
    ![replays20](https://github.com/user-attachments/assets/27462fb6-3560-4324-a82a-33eafaec0c8d)

  - You can right-click on the `config.toml` file and edit it with Notepad
  - Copy the name of your device you got from ffmpeg.
    -  You **must** add `video=` before the name.
    -  So for example, if the camera listed as `Logitech Webcam C930e` you would have

        ```
        ffmpegCamera = 'video=Logitech Webcam C930e'
        ```
    ![replays30](https://github.com/user-attachments/assets/ef454765-8083-401a-b30d-8f9f6fa06e9e)

  



