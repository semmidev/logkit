package slogx_test

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/semmidev/logkit/slogx"
)

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestAsyncWriter_PeriodicFlush(t *testing.T) {
	buf := &safeBuffer{}
	// Set a very short flush interval for testing
	aw := slogx.NewAsyncWriter(buf, 1024, 20*time.Millisecond)
	defer aw.Close()

	n, err := aw.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("failed to write: %v, %d", err, n)
	}

	// Should not have been flushed immediately
	if buf.String() != "" {
		t.Errorf("expected empty buffer initially, got: %s", buf.String())
	}

	// Wait for periodic flush (interval is 20ms, so 50ms should be plenty)
	time.Sleep(50 * time.Millisecond)

	if buf.String() != "hello" {
		t.Errorf("expected periodic flush to write 'hello', got: %s", buf.String())
	}
}

func TestAsyncWriter_FlushOnClose(t *testing.T) {
	buf := &safeBuffer{}
	// Large flush interval so periodic flusher won't trigger in time
	aw := slogx.NewAsyncWriter(buf, 1024, 10*time.Second)

	_, _ = aw.Write([]byte("world"))

	if buf.String() != "" {
		t.Errorf("expected empty buffer initially, got: %s", buf.String())
	}

	if err := aw.Close(); err != nil {
		t.Fatalf("failed to close: %v", err)
	}

	if buf.String() != "world" {
		t.Errorf("expected close to flush buffer, got: %s", buf.String())
	}
}

func TestAsyncWriter_WriteAfterCloseErrors(t *testing.T) {
	buf := &safeBuffer{}
	aw := slogx.NewAsyncWriter(buf, 1024, 10*time.Second)
	if err := aw.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if _, err := aw.Write([]byte("lost")); err == nil {
		t.Fatal("Write after Close must fail, not silently buffer")
	}
}

func TestAsyncWriter_ConcurrentWriteAndClose(t *testing.T) {
	// Close racing Close must not panic ("close of closed channel") and
	// writes racing Close must not disappear silently under -race.
	buf := &safeBuffer{}
	aw := slogx.NewAsyncWriter(buf, 1024, 50*time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = aw.Write([]byte("payload\n"))
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = aw.Close()
		}()
	}
	wg.Wait()

	// last Close result is stored; calling again returns the same verdict
	if err := aw.Close(); err != nil {
		t.Fatalf("repeat Close returned error: %v", err)
	}
}
