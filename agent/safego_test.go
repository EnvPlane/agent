package agent

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestSafeGoRecoversPanicAndRunsOtherTasks(t *testing.T) {
	var logs bytes.Buffer
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
