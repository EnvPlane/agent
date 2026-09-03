package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFluxSourceCommandUsesPersistedTokenWhenReporterTokenIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents/flux-sources/commands/next" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer persisted-runtime-token"; got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	reporter := NewHTTPStatusReporterForAgent(server.URL, "", "cluster", "agent", time.Second)
	cfg := Config{BootstrapProjectID: "project", ClusterID: "cluster", AgentID: "agent", AgentAuthToken: "persisted-runtime-token"}
	if err := runFluxSourceCommandOnce(context.Background(), cfg, reporter, nil); err != nil {
		t.Fatalf("run Flux source command: %v", err)
	}
}
