package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (s *KubernetesNamespaceSource) HasPersistentVolumeClaims(ctx context.Context, namespace string) (bool, error) {
	if err := s.validateWriteNamespace(namespace); err != nil {
		return false, err
	}
	endpoint := strings.TrimRight(s.apiURL, "/") + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/persistentvolumeclaims?limit=1"
	req, err := s.newKubernetesGET(ctx, endpoint)
	if err != nil {
		return false, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("database PVC inventory denied: status=%d", resp.StatusCode)
	}
	var list struct {
		Items    *[]json.RawMessage `json:"items"`
		Metadata struct {
			Continue string `json:"continue"`
		} `json:"metadata"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&list); err != nil {
		return false, err
	}
	if list.Items == nil {
		return false, ErrDatabaseCredentialRecovery
	}
	return len(*list.Items) > 0 || list.Metadata.Continue != "", nil
}

func (s *KubernetesNamespaceSource) CreateGeneratedSecret(ctx context.Context, apply SecretApply) error {
	if err := s.validateWriteNamespace(apply.Namespace); err != nil {
		return err
	}
	if apply.Name == "" || strings.ContainsAny(apply.Name, "/\\") || apply.IdempotencyKey == "" {
		return ErrMaterializationConflict
	}
	payload, err := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": apply.Name, "namespace": apply.Namespace, "labels": apply.Labels, "annotations": apply.Annotations}, "type": apply.Type, "data": encodedSecretData(apply.Data)})
	if err != nil {
		return err
	}
	defer clearMaterialBytes(payload)
	endpoint := strings.TrimRight(s.apiURL, "/") + "/api/v1/namespaces/" + url.PathEscape(apply.Namespace) + "/secrets"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusConflict {
		return ErrMaterializationConflict
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("create generated credential denied: status=%d", resp.StatusCode)
	}
	return nil
}
