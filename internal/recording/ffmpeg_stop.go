package recording

import (
	"io"
	"os"
	"os/exec"
)

func RequestFFmpegStop(cmd *exec.Cmd, stdin io.Writer) error {
	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Signal(os.Interrupt); err == nil {
			return nil
		}
	}

	return RequestFFmpegQuit(stdin)
}

func RequestFFmpegQuit(stdin io.Writer) error {
	if stdin == nil {
		return nil
	}
	_, err := io.WriteString(stdin, "q\n")
	return err
}

func CloseFFmpegStdin(stdin io.Closer) error {
	if stdin == nil {
		return nil
	}
	return stdin.Close()
}
