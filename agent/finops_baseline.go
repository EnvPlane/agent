package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/envplane/contracts/domain"
)

// FetchBaselineBindings accepts only the normal runtime bearer transport; it
// never creates/revokes bindings or manufactures namespace/Environment owners.
func FetchBaselineBindings(ctx context.Context, client *http.Client, endpoint, token, project, cluster, agentID string, generation int64) ([]domain.BaseResourceBinding, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || client == nil || token == "" || project == "" || cluster == "" || agentID == "" || generation < 1 {
		return nil, errors.New("trusted baseline runtime transport required")
	}
	q := url.Values{"projectId": {project}, "clusterId": {cluster}, "agentId": {agentID}}
	u.Path = "/api/v1/agents/finops/base-resource-bindings"
	u.RawQuery = q.Encode()
	u.Fragment = ""
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+token)
	safeClient := *client
	safeClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	safeClient.Timeout = 15 * time.Second
	response, err := safeClient.Do(r)
	if err != nil {
		return nil, errors.New("baseline binding source unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, &FinOpsDeliveryError{StatusCode: response.StatusCode}
	}
	var body struct {
		Bindings []domain.BaseResourceBinding `json:"bindings"`
	}
	d := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 2<<20))
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || body.Bindings == nil || len(body.Bindings) > 2048 || d.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid baseline binding source")
	}
	seen := map[string]bool{}
	for _, b := range body.Bindings {
		if b.Validate() != nil || b.ID == "" || b.ProjectID != project || b.ClusterID != cluster || b.ClusterGeneration != generation || b.Version < 1 || b.State != "active" || b.CreatedAt.IsZero() || seen[b.ResourceUID] {
			return nil, errors.New("baseline binding scope mismatch")
		}
		seen[b.ResourceUID] = true
	}
	return body.Bindings, nil
}

func SubmitBaselineMetering(ctx context.Context, client *http.Client, endpoint, token string, b domain.BaselineMeteringBatch) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || client == nil || token == "" || b.AgentID == "" || len(b.Samples) == 0 {
		return errors.New("trusted baseline runtime transport required")
	}
	data, err := json.Marshal(b)
	if err != nil || len(data) > 1<<20 {
		return errors.New("baseline evidence exceeds transport bound")
	}
	u.Path = "/api/v1/agents/finops/baseline-metering"
	u.RawQuery = ""
	u.Fragment = ""
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	safeClient := *client
	safeClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	safeClient.Timeout = 15 * time.Second
	response, err := safeClient.Do(r)
	if err != nil {
		return &FinOpsDeliveryError{StatusCode: 0}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		return &FinOpsDeliveryError{StatusCode: response.StatusCode}
	}
	return nil
}

func (s *KubernetesNamespaceSource) VerifyBaselineBinding(ctx context.Context, b domain.BaseResourceBinding, project, cluster string, generation int64) error {
	if s == nil || b.Validate() != nil || !s.allowedNamespace(b.Namespace) || b.ProjectID != project || b.ClusterID != cluster || b.ClusterGeneration != generation || b.State != "active" || b.Version < 1 || b.ID == "" {
		return errors.New("baseline binding scope unavailable")
	}
	var ns Namespace
	if err := s.baselineGET(ctx, "/api/v1/namespaces/"+url.PathEscape(b.Namespace), &ns); err != nil || ns.Metadata.Name != b.Namespace || ns.Metadata.Labels[environmentIDLabel] != "" {
		return errors.New("baseline namespace unavailable or environment-owned")
	}
	var name, uid, component string
	switch b.ResourceKind {
	case "PersistentVolumeClaim":
		var pvc finOpsPVC
		if err := s.baselineGET(ctx, "/api/v1/namespaces/"+url.PathEscape(b.Namespace)+"/persistentvolumeclaims/"+url.PathEscape(b.ResourceName), &pvc); err != nil {
			return err
		}
		name = pvc.Metadata.Name
		uid = pvc.Metadata.UID
		component, _ = FinOpsComponentID(pvc.Metadata.Labels)
	case "Pod":
		var pod finOpsPod
		if err := s.baselineGET(ctx, "/api/v1/namespaces/"+url.PathEscape(b.Namespace)+"/pods/"+url.PathEscape(b.ResourceName), &pod); err != nil {
			return err
		}
		if pod.Spec.HostNetwork || finOpsTelemetryPod(pod.Metadata.Labels) {
			return errors.New("shared or unallocated baseline Pod")
		}
		name = pod.Metadata.Name
		uid = pod.Metadata.UID
		component, _ = FinOpsComponentID(pod.Metadata.Labels)
	default:
		return errors.New("unsupported baseline resource")
	}
	if name != b.ResourceName || uid != b.ResourceUID || component != b.ComponentID {
		return errors.New("baseline generation or component changed")
	}
	return nil
}
func (s *KubernetesNamespaceSource) baselineGET(ctx context.Context, path string, out any) error {
	r, err := s.newKubernetesGET(ctx, s.apiURL+path)
	if err != nil {
		return errors.New("baseline metadata unavailable")
	}
	response, err := s.client.Do(r)
	if err != nil {
		return errors.New("baseline metadata unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || json.NewDecoder(http.MaxBytesReader(nil, response.Body, 1<<20)).Decode(out) != nil {
		return errors.New("baseline metadata unavailable")
	}
	return nil
}

// Capacity only, never claimed as used storage or complete baseline coverage.
// The API checks current binding/UID/revoke again when receiving these samples.
func (s *KubernetesNamespaceSource) CollectBaselinePVCCapacity(ctx context.Context, bindings []domain.BaseResourceBinding, project, cluster string, generation int64, start, end time.Time) ([]domain.BaselineMeteringSample, error) {
	if !end.After(start) || end.Sub(start) > 5*time.Minute || len(bindings) > 128 {
		return nil, errors.New("bounded baseline window required")
	}
	samples := []domain.BaselineMeteringSample{}
	for _, b := range bindings {
		if b.ResourceKind != "PersistentVolumeClaim" {
			continue
		}
		if start.Before(b.CreatedAt) || s.VerifyBaselineBinding(ctx, b, project, cluster, generation) != nil {
			return nil, errors.New("baseline binding not current for window")
		}
		var pvc finOpsPVC
		if s.baselineGET(ctx, "/api/v1/namespaces/"+url.PathEscape(b.Namespace)+"/persistentvolumeclaims/"+url.PathEscape(b.ResourceName), &pvc) != nil {
			return nil, errors.New("baseline PVC capacity unavailable")
		}
		component, componentErr := FinOpsComponentID(pvc.Metadata.Labels)
		if componentErr != nil || pvc.Metadata.Name != b.ResourceName || pvc.Metadata.UID != b.ResourceUID || component != b.ComponentID {
			return nil, errors.New("baseline PVC changed during capacity observation")
		}
		if s.VerifyBaselineBinding(ctx, b, project, cluster, generation) != nil {
			return nil, errors.New("baseline changed during observation")
		}
		for i, value := range []string{pvc.Spec.Resources.Requests["storage"], pvc.Status.Capacity["storage"]} {
			if i == 1 && (pvc.Status.Phase != "Bound" || pvc.Spec.VolumeName == "") {
				continue
			}
			quantity, err := finOpsQuantity(value)
			if err != nil || quantity <= 0 {
				return nil, errors.New("baseline capacity unavailable")
			}
			metric := []string{"storage.requested", "storage.provisioned"}[i]
			id := sha256.Sum256([]byte(b.ID + "|" + metric + "|" + start.Format(time.RFC3339Nano) + "|" + end.Format(time.RFC3339Nano)))
			samples = append(samples, domain.BaselineMeteringSample{BaseResourcePin: b.BaseResourcePin, Attribution: domain.FinOpsResourceAttribution{BaseResourceBindingID: b.ID, BindingVersion: b.Version}, SampleID: hex.EncodeToString(id[:]), Metric: metric, Unit: domain.FinOpsGiBHours, MeasurementKind: domain.FinOpsCapacity, Source: "kubernetes-pvc-api", Quantity: quantity / (1 << 30) * end.Sub(start).Hours()})
		}
	}
	return samples, nil
}
