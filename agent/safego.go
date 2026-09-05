package agent

import (
	"log/slog"
	"runtime/debug"
)

// RunSafely isolates a panic in a best-effort background task from the Agent
// process. Callers that need to continue processing work can use its result.
func RunSafely(logger *slog.Logger, task string, fn func()) (panicked bool) {
	if logger == nil {
		logger = slog.Default()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			panicked = true
			logger.Error("agent background task panicked", "task", task, "panic", recovered, "stack", string(debug.Stack()))
		}
	}()
	fn()
	return false
}

// SafeGo starts a best-effort background task with a process-wide panic
// barrier. A panicking task is logged and allowed to stop without taking down
// heartbeat, observation, or other Agent work.
func SafeGo(logger *slog.Logger, task string, fn func()) {
	go func() {
		RunSafely(logger, task, fn)
	}()
}
