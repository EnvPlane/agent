package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/envplane/contracts/domain"
)

type fluxSourceCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (r *HTTPStatusReporter) FetchFluxSourceCommand(ctx context.Context, cfg Config) (*domain.AgentFluxSourceCommand, error) {
	q := url.Values{"projectId": {cfg.BootstrapProjectID}, "clusterId": {cfg.ClusterID}, "agentId": {cfg.AgentID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/api/v1/agents/flux-sources/commands/next?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.AgentAuthToken))
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("fetch Flux source command: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var command domain.AgentFluxSourceCommand
	if err := json.NewDecoder(resp.Body).Decode(&command); err != nil {
		return nil, err
	}
	if err := command.Validate(); err != nil {
		return nil, err
	}
	return &command, nil
}

func (r *HTTPStatusReporter) FetchFluxSourceCredential(ctx context.Context, cfg Config, commandID string) (fluxSourceCredential, error) {
	q := url.Values{"projectId": {cfg.BootstrapProjectID}, "clusterId": {cfg.ClusterID}, "agentId": {cfg.AgentID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/api/v1/agents/flux-sources/commands/"+url.PathEscape(commandID)+"/credential?"+q.Encode(), nil)
	if err != nil {
		return fluxSourceCredential{}, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.AgentAuthToken))
	resp, err := r.client.Do(req)
	if err != nil {
		return fluxSourceCredential{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fluxSourceCredential{}, fmt.Errorf("fetch Flux source credential: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var credential fluxSourceCredential
	if err := json.NewDecoder(resp.Body).Decode(&credential); err != nil {
		return fluxSourceCredential{}, err
	}
	if credential.Username == "" || credential.Password == "" {
		return fluxSourceCredential{}, fmt.Errorf("invalid Flux source credential")
	}
	return credential, nil
}

func (r *HTTPStatusReporter) ReportFluxSourceResult(ctx context.Context, result domain.AgentFluxSourceResult, token string) error {
	return r.postJSONWithBearer(ctx, "/api/v1/agents/flux-sources/commands/"+url.PathEscape(result.CommandID)+"/result", result, strings.TrimSpace(token), "report Flux source result")
}

func RunFluxSourceCommands(ctx context.Context, cfg Config, reporter *HTTPStatusReporter, source *KubernetesNamespaceSource, logger *slog.Logger) {
	if reporter == nil || source == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := runFluxSourceCommandOnce(ctx, cfg, reporter, source); err != nil && logger != nil {
			logger.Warn("Flux source command failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runFluxSourceCommandOnce(ctx context.Context, cfg Config, reporter *HTTPStatusReporter, source *KubernetesNamespaceSource) error {
	if token := strings.TrimSpace(reporter.Token()); token != "" {
		cfg.AgentAuthToken = token
	}
	command, err := reporter.FetchFluxSourceCommand(ctx, cfg)
	if err != nil || command == nil {
		return err
	}
	result := domain.AgentFluxSourceResult{ContractVersion: domain.FluxSourceCommandContractVersion, CommandID: command.CommandID, AttemptID: command.AttemptID, TenantID: command.TenantID, ProjectID: command.ProjectID, ClusterID: command.ClusterID, AgentID: command.AgentID, Status: domain.FluxSourceCommandSucceeded, FinishedAt: time.Now().UTC()}
	credential, err := reporter.FetchFluxSourceCredential(ctx, cfg, command.CommandID)
	if err == nil {
		err = source.applyFluxSource(ctx, *command, credential)
	}
	if err != nil {
		result.Status, result.ErrorCode = domain.FluxSourceCommandFailed, "apply_failed"
	}
	return reporter.ReportFluxSourceResult(ctx, result, cfg.AgentAuthToken)
}

func (s *KubernetesNamespaceSource) applyFluxSource(ctx context.Context, command domain.AgentFluxSourceCommand, credential fluxSourceCredential) error {
	secret := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": command.CredentialSecretName, "namespace": command.Namespace, "labels": map[string]string{"app.kubernetes.io/managed-by": "envplane", "envplane.io/project-id": command.ProjectID}}, "type": "Opaque", "data": map[string]string{"username": base64.StdEncoding.EncodeToString([]byte(credential.Username)), "password": base64.StdEncoding.EncodeToString([]byte(credential.Password))}}
	if err := s.applyFluxObject(ctx, "/api/v1/namespaces/"+url.PathEscape(command.Namespace)+"/secrets", command.CredentialSecretName, secret); err != nil {
		return err
	}
	repository := map[string]any{"apiVersion": "source.toolkit.fluxcd.io/v1", "kind": "GitRepository", "metadata": map[string]any{"name": command.GitRepositoryName, "namespace": command.Namespace, "labels": map[string]string{"app.kubernetes.io/managed-by": "envplane", "envplane.io/project-id": command.ProjectID}}, "spec": map[string]any{"interval": "1m", "url": command.RepositoryURL, "ref": map[string]string{"branch": command.Branch}, "secretRef": map[string]string{"name": command.CredentialSecretName}}}
	if err := s.applyFluxObject(ctx, "/apis/source.toolkit.fluxcd.io/v1/namespaces/"+url.PathEscape(command.Namespace)+"/gitrepositories", command.GitRepositoryName, repository); err != nil {
		return err
	}
	kustomization := map[string]any{"apiVersion": "kustomize.toolkit.fluxcd.io/v1", "kind": "Kustomization", "metadata": map[string]any{"name": command.KustomizationName, "namespace": command.Namespace, "labels": map[string]string{"app.kubernetes.io/managed-by": "envplane", "envplane.io/project-id": command.ProjectID}}, "spec": map[string]any{"interval": "1m", "retryInterval": "1m", "timeout": "10m", "prune": true, "wait": true, "sourceRef": map[string]string{"kind": "GitRepository", "name": command.GitRepositoryName, "namespace": command.Namespace}, "path": command.KustomizationPath}}
	return s.applyFluxObject(ctx, "/apis/kustomize.toolkit.fluxcd.io/v1/namespaces/"+url.PathEscape(command.Namespace)+"/kustomizations", command.KustomizationName, kustomization)
}

func (s *KubernetesNamespaceSource) applyFluxObject(ctx context.Context, collection, name string, object map[string]any) error {
	get, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiURL+collection+"/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	get.Header.Set("Authorization", "Bearer "+s.token)
	if response, getErr := s.client.Do(get); getErr != nil {
		return getErr
	} else {
		if response.StatusCode == http.StatusOK {
			var existing struct {
				Metadata struct {
					Labels map[string]string `json:"labels"`
				} `json:"metadata"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&existing)
			_ = response.Body.Close()
			if decodeErr != nil {
				return decodeErr
			}
			metadata := object["metadata"].(map[string]any)
			labels := metadata["labels"].(map[string]string)
			if existing.Metadata.Labels["app.kubernetes.io/managed-by"] != "envplane" || existing.Metadata.Labels["envplane.io/project-id"] != labels["envplane.io/project-id"] {
				return fmt.Errorf("refuse to overwrite foreign Flux source resource")
			}
		} else {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				return fmt.Errorf("read Flux source: status=%d", response.StatusCode)
			}
		}
	}
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	endpoint := s.apiURL + collection + "/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/apply-patch+yaml")
	req.Header.Set("Accept", "application/json")
	query := req.URL.Query()
	query.Set("fieldManager", "envplane-flux-source")
	req.URL.RawQuery = query.Encode()
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("apply Flux source: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
