package main

import (
	"fmt"
	"os/exec"
	"time"
)

func main() {
	// Start the ffmpeg process
	cmd := exec.Command("/mnt/c/ProgramData/chocolatey/bin/ffmpeg.exe", "-f", "dshow", "-i", "video=OV01A", "output.mp4")

	// Create a pipe to send input to ffmpeg
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Println("Error creating stdin pipe:", err)
		return
	}

	// Start the command in the background
	if err := cmd.Start(); err != nil {
		fmt.Println("Error starting ffmpeg:", err)
		return
	}

	// Let ffmpeg run for 10 seconds
	time.Sleep(5 * time.Second)

	// Send the 'q' key press to ffmpeg
	_, err = stdin.Write([]byte("q"))
	if err != nil {
		fmt.Println("Error sending 'q' to ffmpeg:", err)
		return
	}

	// Close the stdin pipe to ensure ffmpeg receives EOF
	if err := stdin.Close(); err != nil {
		fmt.Println("Error closing stdin pipe:", err)
		return
	}

	// Wait for the process to exit
	if err := cmd.Wait(); err != nil {
		fmt.Println("Error waiting for ffmpeg to finish:", err)
		return
	}

	fmt.Println("Recording stopped and file saved.")
}
