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
	command := domain.AgentSecretMaterializationCommand{ContractVersion: domain.SecretMaterializationCommandContractVersion, CommandID: "command", TenantID: plan.TenantID, ProjectID: plan.ProjectID, EnvironmentID: plan.EnvironmentID, ClusterID: "cluster", AgentID: "agent", Operation: domain.SecretOperationMaterialize, PlanID: plan.PlanID, PlanDigest: plan.Digest, ExpectedRevision: plan.Revision, Plan: plan, Status: domain.SecretCommandClaimed, Attempt: 1, AttemptID: "attempt", CreatedAt: time.Unix(2, 0), EnvelopeLeases: map[string]domain.SecretMaterializationEnvelopeLease{"clone": {LeaseID: "lease", EnvelopeDigest: "sha256:digest", Audience: "agent", ExpiresAt: time.Now().UTC().Add(time.Hour)}}}
	var reported domain.AgentSecretMaterializationResult
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runtime-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(command)
		case http.MethodPost:
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
	reporter := NewHTTPStatusReporterForAgent(server.URL, "stale-token", "cluster", "agent", time.Second)
	// Runtime recovery updates the shared reporter while this loop still owns
	// its startup Config snapshot. Materialization must use the new token.
	reporter.SetToken("runtime-token")
	cfg := Config{ControlPlaneURL: server.URL, BootstrapProjectID: plan.ProjectID, ClusterID: "cluster", AgentID: "agent", AgentAuthToken: "stale-token"}
	if err := runSecretMaterializationCommandOnce(context.Background(), cfg, reporter, materializer, nil); err != nil {
		t.Fatal(err)
	}
	if reported.Status != domain.SecretCommandSucceeded || reported.CommandID != command.CommandID || len(reported.Items) != 1 || reported.Items[0].Status != domain.SecretItemReady {
		t.Fatalf("reported result = %#v", reported)
	}
	wantKey, err := domain.SecretMaterializationIdempotencyKey(plan.TenantID, plan.ProjectID, plan.EnvironmentID, plan.TemplateDigest, plan.TargetNamespace, "clone", domain.SecretOperationMaterialize)
	if err != nil {
		t.Fatal(err)
	}
	if reported.Items[0].IdempotencyKey != wantKey {
		t.Fatalf("result idempotency key=%q, want shared-contract key %q", reported.Items[0].IdempotencyKey, wantKey)
	}
}

func TestSecretMaterializationRuntimeRejectsExpiredEnvelopeLease(t *testing.T) {
	plan := materializerPlan(t, []domain.SecretStrategyConfig{{ID: "clone", Strategy: domain.SecretStrategyEncryptedClone, SourceNamespace: "base", SourceName: "source", TargetNamespace: "target", TargetName: "clone", EncryptedPayloadRef: "envelopes/clone"}})
	now := time.Now().UTC()
	command := domain.AgentSecretMaterializationCommand{ContractVersion: domain.SecretMaterializationCommandContractVersion, AgentID: "agent", Operation: domain.SecretOperationMaterialize, Plan: plan, EnvelopeLeases: map[string]domain.SecretMaterializationEnvelopeLease{"clone": {LeaseID: "lease", EnvelopeDigest: "sha256:digest", Audience: "agent", ExpiresAt: now}}}
	if err := validateSecretMaterializationEnvelopeLeases(command, now); err == nil {
		t.Fatal("expired envelope lease was accepted")
	}
}

func TestSecretMaterializationRuntimeReportsFailedItemFromPartialExecution(t *testing.T) {
	plan := materializerPlan(t, []domain.SecretStrategyConfig{{ID: "clone", Strategy: domain.SecretStrategyEncryptedClone, SourceNamespace: "base", SourceName: "source", TargetNamespace: "target", TargetName: "clone", EncryptedPayloadRef: "envelopes/clone"}})
	command := domain.AgentSecretMaterializationCommand{ContractVersion: domain.SecretMaterializationCommandContractVersion, CommandID: "command", TenantID: plan.TenantID, ProjectID: plan.ProjectID, EnvironmentID: plan.EnvironmentID, ClusterID: "cluster", AgentID: "agent", Operation: domain.SecretOperationMaterialize, PlanID: plan.PlanID, PlanDigest: plan.Digest, ExpectedRevision: plan.Revision, Plan: plan, Status: domain.SecretCommandClaimed, Attempt: 1, AttemptID: "attempt", CreatedAt: time.Unix(2, 0), EnvelopeLeases: map[string]domain.SecretMaterializationEnvelopeLease{"clone": {LeaseID: "lease", EnvelopeDigest: "sha256:digest", Audience: "agent", ExpiresAt: time.Now().UTC().Add(time.Hour)}}}
	var reported domain.AgentSecretMaterializationResult
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(command)
		case http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&reported); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	fake := &materializerFake{secrets: map[string]SecretRecord{
		"base/source":  {Type: "Opaque", Data: map[string][]byte{"key": {0x01}}},
		"target/clone": {Type: "Opaque", Labels: map[string]string{"app.kubernetes.io/managed-by": "foreign"}},
	}}
	materializer, err := NewSecretMaterializer(fake, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	reporter := NewHTTPStatusReporterForAgent(server.URL, "runtime-token", "cluster", "agent", time.Second)
	cfg := Config{ControlPlaneURL: server.URL, BootstrapProjectID: plan.ProjectID, ClusterID: "cluster", AgentID: "agent", AgentAuthToken: "runtime-token"}
	if err := runSecretMaterializationCommandOnce(context.Background(), cfg, reporter, materializer, nil); err != nil {
		t.Fatal(err)
	}
	if reported.Status != domain.SecretCommandFailed || reported.ErrorCode != domain.SecretErrorConflict || len(reported.Items) != 1 || reported.Items[0].Status != domain.SecretItemFailed || reported.Items[0].ErrorCode != domain.SecretErrorConflict {
		t.Fatalf("reported partial failure = %#v", reported)
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
