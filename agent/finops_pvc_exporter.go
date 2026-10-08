package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

// VerifyPinnedPVCUsageRef requires GET only on the approved names. It does not
// read Secrets, datasets, pods/exec, nodes/proxy or host paths.
func (s *KubernetesNamespaceSource) VerifyPinnedPVCUsageRef(ctx context.Context, ref PinnedPVCUsageRef) error {
	if s == nil || !s.allowedNamespace(ref.Namespace) {
		return errors.New("pinned namespace unavailable")
	}
	req, err := s.newKubernetesGET(ctx, s.apiURL+"/api/v1/namespaces/"+url.PathEscape(ref.Namespace)+"/persistentvolumeclaims/"+url.PathEscape(ref.PVCName))
	if err != nil {
		return err
	}
	response, err := s.client.Do(req)
	if err != nil {
		return errors.New("pinned PVC lookup unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	var pvc finOpsPVC
	if response.StatusCode != http.StatusOK || json.NewDecoder(http.MaxBytesReader(nil, response.Body, 1<<20)).Decode(&pvc) != nil {
		return errors.New("pinned PVC lookup unavailable")
	}
	component, err := FinOpsComponentID(pvc.Metadata.Labels)
	if err != nil || pvc.Metadata.UID != ref.PVCUID || pvc.Metadata.Name != ref.PVCName || component != ref.ComponentID || pvc.Status.Phase != "Bound" || pvc.Spec.VolumeName == "" {
		return errors.New("pinned PVC generation/component mismatch")
	}
	return nil
}

// NewPinnedPVCUsageHandler must be served by an operator-reviewed TLS server.
// Non-TLS requests fail closed. Identity/permission failures never publish an
// old reading or synthesize zero usage.
func NewPinnedPVCUsageHandler(sampler *PinnedPVCUsageSampler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.Method != http.MethodGet || r.URL.Path != "/metrics" || sampler == nil {
			http.Error(w, "secure approved metrics request required", http.StatusForbidden)
			return
		}
		readings, err := sampler.Sample(r.Context())
		if err != nil {
			http.Error(w, "PVC measurement unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(PVCUsagePrometheusText(readings)))
	})
}
