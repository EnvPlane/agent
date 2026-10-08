package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const databaseEscrowPVCMarker = "envplane.io/database-credential-escrow"

type databasePVCRecord struct {
	Metadata struct {
		UID             string            `json:"uid"`
		Name            string            `json:"name"`
		Namespace       string            `json:"namespace"`
		ResourceVersion string            `json:"resourceVersion"`
		Annotations     map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		VolumeName string `json:"volumeName"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

func (s *KubernetesNamespaceSource) databasePVCRecord(ctx context.Context, pvc DatabasePVCIdentity) (databasePVCRecord, string, error) {
	var record databasePVCRecord
	if err := s.validateWriteNamespace(pvc.Namespace); err != nil {
		return record, "", ErrDatabaseCredentialRecovery
	}
	endpoint := strings.TrimRight(s.apiURL, "/") + "/api/v1/namespaces/" + url.PathEscape(pvc.Namespace) + "/persistentvolumeclaims/" + url.PathEscape(pvc.Name)
	req, err := s.newKubernetesGET(ctx, endpoint)
	if err != nil {
		return record, "", ErrDatabaseCredentialRecovery
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return record, "", ErrDatabaseCredentialRecovery
	}
	defer func() { _ = resp.Body.Close() }()
	err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&record)
	if resp.StatusCode != http.StatusOK || err != nil || record.Metadata.UID != pvc.UID || record.Metadata.Name != pvc.Name || record.Metadata.Namespace != pvc.Namespace || record.Spec.VolumeName != pvc.VolumeName || record.Status.Phase != "Bound" || record.Metadata.ResourceVersion == "" {
		return record, "", ErrDatabaseCredentialRecovery
	}
	return record, endpoint, nil
}

func (s *KubernetesNamespaceSource) DatabasePVCInitializationConsumed(ctx context.Context, binding DatabaseCredentialBinding) (bool, error) {
	if _, err := canonicalDatabaseBinding(binding); err != nil {
		return false, err
	}
	for _, pvc := range binding.PVCs {
		record, _, err := s.databasePVCRecord(ctx, pvc)
		if err != nil {
			return false, err
		}
		if _, marked := record.Metadata.Annotations[databaseEscrowPVCMarker]; marked {
			return true, nil
		}
	}
	return false, nil
}

func (s *KubernetesNamespaceSource) MarkDatabasePVCInitialization(ctx context.Context, binding DatabaseCredentialBinding, locator string) error {
	if _, err := canonicalDatabaseBinding(binding); err != nil || locator == "" {
		return ErrDatabaseCredentialRecovery
	}
	for _, pvc := range binding.PVCs {
		record, endpoint, err := s.databasePVCRecord(ctx, pvc)
		if err != nil {
			return err
		}
		if previous, marked := record.Metadata.Annotations[databaseEscrowPVCMarker]; marked {
			if previous != locator {
				return ErrDatabaseCredentialRecovery
			}
			continue
		}
		patch := []map[string]any{
			{"op": "test", "path": "/metadata/uid", "value": pvc.UID},
			{"op": "test", "path": "/metadata/resourceVersion", "value": record.Metadata.ResourceVersion},
		}
		if record.Metadata.Annotations == nil {
			patch = append(patch, map[string]any{"op": "add", "path": "/metadata/annotations", "value": map[string]string{}})
		}
		patch = append(patch, map[string]any{"op": "add", "path": "/metadata/annotations/envplane.io~1database-credential-escrow", "value": locator})
		payload, err := json.Marshal(patch)
		if err != nil {
			return ErrDatabaseCredentialRecovery
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(payload))
		if err != nil {
			return ErrDatabaseCredentialRecovery
		}
		req.Header.Set("Content-Type", "application/json-patch+json")
		if s.token != "" {
			req.Header.Set("Authorization", "Bearer "+s.token)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return ErrDatabaseCredentialRecovery
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return ErrDatabaseCredentialRecovery
		}
	}
	return nil
}
