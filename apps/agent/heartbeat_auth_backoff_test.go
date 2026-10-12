package main

import (
	"errors"
	"fmt"
	"testing"
	"time"

	clusteragent "github.com/envplane/agent/agent"
)

func TestHeartbeatAuthBackoffIsBoundedAndTyped(t *testing.T) {
	for _, status := range []int{401, 403, 404} {
		if !isHeartbeatAuthDenied(fmt.Errorf("wrapped: %w", &clusteragent.APIError{Status: status, Code: "not_found"})) {
			t.Fatalf("status %d not recognized", status)
		}
	}
	for _, err := range []error{errors.New("not_found password=secret"), &clusteragent.APIError{Status: 500, Code: "not_found"}, nil} {
		if isHeartbeatAuthDenied(err) {
			t.Fatal("untyped or non-auth failure classified as identity denial")
		}
	}
	for i, expected := range []time.Duration{0, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute} {
		if actual := heartbeatAuthRetryDelay(i); actual != expected {
			t.Fatalf("failure %d: delay %v want %v", i, actual, expected)
		}
	}
}
