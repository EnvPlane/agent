package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// KubernetesNamespaceSource implements the narrow materializer client. It
// never lists Secrets and only issues named GETs for approved source refs.
func (s *KubernetesNamespaceSource) GetSecret(ctx context.Context, namespace, name string) (SecretRecord, error) {
	if !s.allowedNamespace(namespace) || strings.TrimSpace(name) == "" || strings.ContainsAny(name, "/\\") {
		return SecretRecord{}, ErrSecretNotFound
	}
	endpoint := strings.TrimRight(s.apiURL, "/") + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/secrets/" + url.PathEscape(name)
	request, err := s.newKubernetesGET(ctx, endpoint)
	if err != nil {
		return SecretRecord{}, err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return SecretRecord{}, fmt.Errorf("read approved Secret %s/%s: %w", namespace, name, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return SecretRecord{}, fmt.Errorf("read approved Secret %s/%s: %w", namespace, name, err)
	}
	if response.StatusCode == http.StatusNotFound {
		return SecretRecord{}, ErrSecretNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SecretRecord{}, fmt.Errorf("read approved Secret %s/%s denied: status=%d", namespace, name, response.StatusCode)
	}
	var raw struct {
		Type     string            `json:"type"`
		Data     map[string]string `json:"data"`
		Metadata struct {
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return SecretRecord{}, fmt.Errorf("decode approved Secret %s/%s: %w", namespace, name, err)
	}
	data := make(map[string][]byte, len(raw.Data))
	for key, encoded := range raw.Data {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return SecretRecord{}, fmt.Errorf("decode approved Secret %s/%s data: %w", namespace, name, err)
		}
		data[key] = decoded
	}
	return SecretRecord{Type: raw.Type, Data: data, Labels: raw.Metadata.Labels, Annotations: raw.Metadata.Annotations}, nil
}

func (s *KubernetesNamespaceSource) ApplySecret(ctx context.Context, apply SecretApply) error {
	body := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": apply.Name, "namespace": apply.Namespace, "labels": apply.Labels, "annotations": apply.Annotations}, "type": apply.Type, "data": encodedSecretData(apply.Data)}
	return s.applyResource(ctx, "/api/v1/namespaces/"+url.PathEscape(apply.Namespace)+"/secrets/"+url.PathEscape(apply.Name), body, apply)
}

func (s *KubernetesNamespaceSource) ApplyExternal(ctx context.Context, apply SecretApply) error {
	body := map[string]any{"apiVersion": "external-secrets.io/v1beta1", "kind": "ExternalSecret", "metadata": map[string]any{"name": apply.Name, "namespace": apply.Namespace, "labels": apply.Labels, "annotations": apply.Annotations}, "spec": map[string]any{"refreshInterval": "1h", "secretStoreRef": map[string]any{"name": apply.ExternalStore, "kind": "SecretStore"}, "target": map[string]any{"name": apply.Name}, "data": []any{map[string]any{"secretKey": "value", "remoteRef": map[string]any{"key": apply.ExternalKey}}}}}
	return s.applyResource(ctx, "/apis/external-secrets.io/v1beta1/namespaces/"+url.PathEscape(apply.Namespace)+"/externalsecrets/"+url.PathEscape(apply.Name), body, apply)
}

func (s *KubernetesNamespaceSource) DeleteSecret(ctx context.Context, namespace, name string) error {
	if !s.allowedNamespace(namespace) || strings.TrimSpace(name) == "" || strings.ContainsAny(name, "/\\") {
		return ErrSecretNotFound
	}
	endpoint := strings.TrimRight(s.apiURL, "/") + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/secrets/" + url.PathEscape(name)
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return ErrSecretNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("delete owned Secret %s/%s denied: status=%d", namespace, name, response.StatusCode)
	}
	return nil
}

func (s *KubernetesNamespaceSource) applyResource(ctx context.Context, resourcePath string, body map[string]any, apply SecretApply) error {
	if strings.TrimSpace(apply.Namespace) == "" || strings.TrimSpace(apply.Name) == "" || strings.TrimSpace(apply.FieldManager) == "" || strings.TrimSpace(apply.IdempotencyKey) == "" {
		return ErrMaterializationConflict
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(s.apiURL, "/") + path.Clean("/"+resourcePath)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	query := parsed.Query()
	query.Set("fieldManager", apply.FieldManager)
	query.Set("force", fmt.Sprintf("%t", apply.Force))
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch, parsed.String(), strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/apply-patch+yaml")
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode == http.StatusConflict {
		return ErrMaterializationConflict
	}
	return fmt.Errorf("apply materialization resource denied: status=%d", response.StatusCode)
}

func encodedSecretData(data map[string][]byte) map[string]string {
	output := make(map[string]string, len(data))
	for key, value := range data {
		output[key] = base64.StdEncoding.EncodeToString(value)
	}
	return output
}
