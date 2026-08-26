package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/envplane/contracts/domain"
)

func TestSecretMaterializationRuntimeClaimsExecutesAndReportsWithAgentAuth(t *testing.T) {
	plan := materializerPlan(t, []domain.SecretStrategyConfig{{ID: "clone", Strategy: domain.SecretStrategyEncryptedClone, SourceNamespace: "base", SourceName: "source", TargetNamespace: "target", TargetName: "clone", EncryptedPayloadRef: "envelopes/clone"}})
	command := domain.AgentSecretMaterializationCommand{ContractVersion: domain.SecretMaterializationCommandContractVersion, CommandID: "command", TenantID: plan.TenantID, ProjectID: plan.ProjectID, EnvironmentID: plan.EnvironmentID, ClusterID: "cluster", AgentID: "agent", Operation: domain.SecretOperationMaterialize, PlanID: plan.PlanID, PlanDigest: plan.Digest, ExpectedRevision: plan.Revision, Plan: plan, Status: domain.SecretCommandClaimed, Attempt: 1, AttemptID: "attempt", CreatedAt: time.Unix(2, 0)}
	var reported domain.AgentSecretMaterializationResult
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runtime-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(command)
		case r.Method == http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&reported); err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	fake := &materializerFake{secrets: map[string]SecretRecord{"base/source": {Type: "Opaque", Data: map[string][]byte{"key": {0x01}}}}}
	materializer, err := NewSecretMaterializer(fake, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	reporter := NewHTTPStatusReporterForAgent(server.URL, "runtime-token", "cluster", "agent", time.Second)
	cfg := Config{ControlPlaneURL: server.URL, BootstrapProjectID: plan.ProjectID, ClusterID: "cluster", AgentID: "agent", AgentAuthToken: "runtime-token"}
	if err := runSecretMaterializationCommandOnce(context.Background(), cfg, reporter, materializer, nil); err != nil {
		t.Fatal(err)
	}
	if reported.Status != domain.SecretCommandSucceeded || reported.CommandID != command.CommandID || len(reported.Items) != 1 || reported.Items[0].Status != domain.SecretItemReady {
		t.Fatalf("reported result = %#v", reported)
	}
}

func TestMaterializationWireItemErrorCodeIsCanonical(t *testing.T) {
	tests := map[string]domain.SecretMaterializationErrorCode{
		"foreign_secret":     domain.SecretErrorConflict,
		"unsafe_secret_type": domain.SecretErrorValidationFailed,
		"source_not_found":   domain.SecretErrorSourceNotFound,
		"unexpected":         domain.SecretErrorBackendUnavailable,
	}
	for input, want := range tests {
		if got := materializationWireItemErrorCode(input); got != want {
			t.Fatalf("materializationWireItemErrorCode(%q) = %q, want %q", input, got, want)
		}
	}
}
