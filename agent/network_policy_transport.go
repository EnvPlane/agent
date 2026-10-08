package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/envplane/contracts/domain"
)

var ErrProbeTransport = errors.New("network policy evidence transport unavailable")

type NetworkPolicyProbeTransport struct {
	baseURL  string
	token    string
	identity domain.NetworkPolicyProbeIdentity
	client   *http.Client
}

// Only a runtime Agent token belongs here. TLS is mandatory even when ordinary
// local Agent heartbeat has an explicit insecure-development exception.
func NewNetworkPolicyProbeTransport(baseURL, token string, identity domain.NetworkPolicyProbeIdentity, client *http.Client) (*NetworkPolicyProbeTransport, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(token) == "" || identity.ProjectID == "" || identity.ClusterID == "" || identity.AgentID == "" {
		return nil, ErrProbeTransport
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if transport, ok := copyClient.Transport.(*http.Transport); ok && transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		return nil, ErrProbeTransport
	}
	return &NetworkPolicyProbeTransport{baseURL: strings.TrimRight(baseURL, "/"), token: strings.TrimSpace(token), identity: identity, client: &copyClient}, nil
}

func (t *NetworkPolicyProbeTransport) post(ctx context.Context, path string, body, out any, wantStatus int) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return ErrProbeTransport
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(bounded, http.MethodPost, t.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return ErrProbeTransport
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return ErrProbeTransport
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("probe transfer rejected: status=%d", resp.StatusCode)
	}
	if out != nil && json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(out) != nil {
		return ErrProbeTransport
	}
	return nil
}

func (t *NetworkPolicyProbeTransport) Challenge(ctx context.Context, clusterUID string) (domain.NetworkPolicyProbeChallenge, error) {
	var challenge domain.NetworkPolicyProbeChallenge
	err := t.post(ctx, "/api/v1/agents/network-policy/probes/challenge", domain.NetworkPolicyProbeChallengeRequest{NetworkPolicyProbeIdentity: t.identity, ClusterUID: clusterUID}, &challenge, http.StatusCreated)
	if err != nil {
		return challenge, err
	}
	if len(challenge.Nonce) != 64 || len(challenge.ProbeID) != 32 || challenge.Generation <= 0 || challenge.IssuedAt.IsZero() || !challenge.ExpiresAt.After(challenge.IssuedAt) || !time.Now().Before(challenge.ExpiresAt) {
		return domain.NetworkPolicyProbeChallenge{}, ErrProbeTransport
	}
	return challenge, nil
}

func (t *NetworkPolicyProbeTransport) Submit(ctx context.Context, challenge domain.NetworkPolicyProbeChallenge, report domain.NetworkPolicyProbeReport) error {
	if report.Validate() != nil || report.Generation != challenge.Generation {
		return ErrProbeTransport
	}
	return t.post(ctx, "/api/v1/agents/network-policy/probes/result", domain.NetworkPolicyProbeSubmission{NetworkPolicyProbeIdentity: t.identity, ProbeID: challenge.ProbeID, Nonce: challenge.Nonce, Report: report}, nil, http.StatusAccepted)
}
