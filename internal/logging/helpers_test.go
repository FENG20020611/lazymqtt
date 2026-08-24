package logging

import (
	"io"
	stdlog "log"
)

// stdlogPrintln goes through the standard log package on purpose: the point of
// the test using it is that Setup has redirected that package away from the
// terminal.
func stdlogPrintln(s string) { stdlog.Println(s) }

func copyAll(dst io.Writer, src io.Reader) (int64, error) { return io.Copy(dst, src) }
