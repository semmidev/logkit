package slogx

import (
	"bufio"
	"io"
	"sync"
	"time"
)

// AsyncWriter is a thread-safe, asynchronous buffered writer.
// It wraps an underlying io.Writer and flushes its buffer periodically
// in a background goroutine, or when the buffer is full, or when Sync/Flush/Close is called.
type AsyncWriter struct {
	mu            sync.Mutex
	buf           *bufio.Writer
	w             io.Writer
	stop          chan struct{}
	done          chan struct{}
	flushInterval time.Duration
}

// NewAsyncWriter creates a new AsyncWriter wrapping w.
// bufferSize is the size of the internal buffer in bytes (e.g. 4096).
// flushInterval is how often the background goroutine flushes the buffer (e.g. 100ms).
func NewAsyncWriter(w io.Writer, bufferSize int, flushInterval time.Duration) *AsyncWriter {
	if bufferSize <= 0 {
		bufferSize = 4096
	}
	if flushInterval <= 0 {
		flushInterval = 100 * time.Millisecond
	}

	aw := &AsyncWriter{
		buf:           bufio.NewWriterSize(w, bufferSize),
		w:             w,
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		flushInterval: flushInterval,
	}

	go aw.flusher()
	return aw
}

// Write writes data to the internal buffer. If the buffer fills up, it writes to the underlying writer.
func (aw *AsyncWriter) Write(p []byte) (n int, err error) {
	aw.mu.Lock()
	defer aw.mu.Unlock()
	return aw.buf.Write(p)
}

// Flush writes any buffered data to the underlying writer.
func (aw *AsyncWriter) Flush() error {
	aw.mu.Lock()
	defer aw.mu.Unlock()
	return aw.buf.Flush()
}

// Sync is an alias for Flush to implement the Syncer interface.
func (aw *AsyncWriter) Sync() error {
	return aw.Flush()
}

// Close flushes the buffer, stops the background flushing goroutine, and closes the underlying writer if it implements io.Closer.
func (aw *AsyncWriter) Close() error {
	select {
	case <-aw.stop:
		// Already closed
		return nil
	default:
	}

	close(aw.stop)
	<-aw.done

	err := aw.Flush()

	if closer, ok := aw.w.(io.Closer); ok {
		if closeErr := closer.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return err
}

func (aw *AsyncWriter) flusher() {
	defer close(aw.done)
	ticker := time.NewTicker(aw.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = aw.Flush()
		case <-aw.stop:
			return
		}
	}
}
