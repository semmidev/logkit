package slogx

import (
	"bufio"
	"errors"
	"io"
	"sync"
	"time"
)

// ErrClosed is returned by Write after Close has been called, instead of
// silently dropping the bytes into a buffer that will never be flushed.
var ErrClosed = errors.New("slogx: AsyncWriter is closed")

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
	closeOnce     sync.Once
	closeErr      error
	closed        bool
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
	if aw.closed {
		return 0, ErrClosed
	}
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

// Close flushes the buffer, stops the background flushing goroutine, and
// closes the underlying writer if it implements io.Closer. Safe to call
// multiple times, including concurrently: only the first call does the
// work, later calls return the same result. Writes racing Close either
// land before the final flush, or get ErrClosed back instead of being
// dropped silently.
func (aw *AsyncWriter) Close() error {
	aw.closeOnce.Do(func() {
		close(aw.stop)
		<-aw.done

		aw.mu.Lock()
		aw.closed = true
		err := aw.buf.Flush()
		if closer, ok := aw.w.(io.Closer); ok {
			if closeErr := closer.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}
		aw.mu.Unlock()
		aw.closeErr = err
	})
	return aw.closeErr
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
