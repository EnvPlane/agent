package agent

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func TestSafeGoRecoversPanicAndRunsOtherTasks(t *testing.T) {
	var logs lockedBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	finished := make(chan struct{})
	SafeGo(logger, "panic-task", func() { panic("unexpected resource shape") })
	SafeGo(logger, "healthy-task", func() { close(finished) })
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("healthy task did not run after another task panicked")
	}
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(logs.String(), "agent background task panicked") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if output := logs.String(); !strings.Contains(output, "agent background task panicked") || !strings.Contains(output, "panic-task") || !strings.Contains(output, "goroutine") {
		t.Fatalf("panic log = %q", output)
	}
}
