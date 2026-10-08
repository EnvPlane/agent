package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/envplane/contracts/domain"
)

func TestProbeTransportRequiresTLSAndRefusesRedirects(t *testing.T) {
	identity := domain.NetworkPolicyProbeIdentity{ProjectID: "p", ClusterID: "c", AgentID: "a"}
	if _, err := NewNetworkPolicyProbeTransport("http://localhost:3000", "token", identity, nil); err == nil {
		t.Fatal("insecure transport accepted")
	}
	seen := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		w.Header().Set("Location", "https://example.invalid/credential-target")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	transport, err := NewNetworkPolicyProbeTransport(server.URL, "runtime-token", identity, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transport.Challenge(context.Background(), "uid"); err == nil || strings.Contains(err.Error(), "runtime-token") {
		t.Fatal("redirect accepted or bearer leaked")
	}
	if seen != 1 {
		t.Fatal("unexpected redirect retry")
	}
}

func TestProbeTransportChallengeAndSubmitUseRuntimeBearer(t *testing.T) {
	id := domain.NetworkPolicyProbeIdentity{ProjectID: "p", ClusterID: "c", AgentID: "a"}
	now := time.Now().UTC()
	c := domain.NetworkPolicyProbeChallenge{ProbeID: strings.Repeat("a", 32), Nonce: strings.Repeat("b", 64), Generation: 4, IssuedAt: now, ExpiresAt: now.Add(time.Minute)}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runtime-token" {
			t.Error("missing bearer")
		}
		switch r.URL.Path {
		case "/api/v1/agents/network-policy/probes/challenge":
			var req domain.NetworkPolicyProbeChallengeRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.NetworkPolicyProbeIdentity != id || req.ClusterUID != "uid" {
				t.Error("identity binding missing")
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(c)
		case "/api/v1/agents/network-policy/probes/result":
			var req domain.NetworkPolicyProbeSubmission
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.Nonce != c.Nonce || req.ProbeID != c.ProbeID || req.Report.Generation != 4 {
				t.Error("challenge binding missing")
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	transport, err := NewNetworkPolicyProbeTransport(server.URL, "runtime-token", id, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := transport.Challenge(context.Background(), "uid")
	if err != nil {
		t.Fatal(err)
	}
	report := domain.NetworkPolicyProbeReport{SchemaVersion: 1, ClusterUID: "uid", Generation: 4, CheckedAt: now, Scope: "single-node-pod-ipv4-tcp", State: "passed", Ingress: "passed", Egress: "passed", CleanupComplete: true, Reason: "measured"}
	if err = transport.Submit(context.Background(), challenge, report); err != nil {
		t.Fatal(err)
	}
}
