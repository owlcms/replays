package recording

import "io"

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
