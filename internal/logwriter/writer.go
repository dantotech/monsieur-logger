// Package logwriter provides a synchronized, buffered line writer for stdout.
//
// All log output of monsieur-logger (test logs and application logs) goes
// through a single Writer so that lines are never interleaved, even when
// several test runs are active at the same time.
package logwriter

import (
	"bufio"
	"io"
	"sync"
)

// Writer serializes newline-terminated records to an underlying io.Writer.
type Writer struct {
	mu sync.Mutex
	bw *bufio.Writer
}

// New returns a Writer wrapping w with a 256 KiB buffer.
func New(w io.Writer) *Writer {
	return &Writer{bw: bufio.NewWriterSize(w, 256*1024)}
}

// WriteLine appends line plus a trailing newline to the buffer.
// The data is not flushed; call Flush after a burst of lines.
func (w *Writer) WriteLine(line []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.bw.Write(line); err != nil {
		return err
	}
	return w.bw.WriteByte('\n')
}

// Flush writes any buffered data to the underlying writer.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bw.Flush()
}

// Write implements io.Writer. p must already contain complete line(s)
// (e.g. as produced by slog.JSONHandler). The data is flushed immediately.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.bw.Write(p)
	if err != nil {
		return n, err
	}
	return n, w.bw.Flush()
}
