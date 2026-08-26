package main

import (
	"errors"
	"testing"

	clusteragent "github.com/envplane/agent/agent"
)

func TestIsAgentAuthTokenNotIssuedErrorRecognizesPlainResponse(t *testing.T) {
	err := errors.New(`report heartbeat failed: status=401 body={"error":"agent auth token is not issued for project \"envplane\""}`)
	if !isAgentAuthTokenNotIssuedError(err) {
		t.Fatal("expected plain 401 response to trigger runtime auth recovery")
	}
}

func TestIsAgentAuthTokenNotIssuedErrorRecognizesAPIError(t *testing.T) {
	err := &clusteragent.APIError{Status: 401, Code: "agent_auth_invalid", Message: "agent auth token is not issued for project \"envplane\""}
	if !isAgentAuthTokenNotIssuedError(err) {
		t.Fatal("expected API error to trigger runtime auth recovery")
	}
}

func TestIsAgentAuthTokenNotIssuedErrorRecognizesInvalidAPIToken(t *testing.T) {
	err := errors.New(`report heartbeat failed: status=401 body={"error":"invalid api token"}`)
	if !isAgentAuthTokenNotIssuedError(err) {
		t.Fatal("expected invalid API token response to trigger runtime auth recovery")
	}
}
