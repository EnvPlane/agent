package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/envplane/contracts/domain"
)

type finOpsPod struct {
	Spec struct {
		HostNetwork bool `json:"hostNetwork"`
		Containers  []struct {
			Name      string `json:"name"`
			Resources struct {
				Requests map[string]string `json:"requests"`
			} `json:"resources"`
		} `json:"containers"`
	} `json:"spec"`
	Metadata struct {
		Name      string            `json:"name"`
		UID       string            `json:"uid"`
		CreatedAt time.Time         `json:"creationTimestamp"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
}
type finOpsMetric struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Timestamp  time.Time `json:"timestamp"`
	Window     string    `json:"window"`
	Containers []struct {
		Name  string            `json:"name"`
		Usage map[string]string `json:"usage"`
	} `json:"containers"`
}

// CollectFinOps uses Metrics API windows, not the poll interval. Missing metrics
// or attribution is explicit; no namespace-name or base-service guessing.
func (s *KubernetesNamespaceSource) CollectFinOps(ctx context.Context, project, cluster, agentID string, now time.Time) (domain.FinOpsMeteringBatch, error) {
	b := domain.FinOpsMeteringBatch{ProjectID: project, ClusterID: cluster, AgentID: agentID, PeriodStart: now.Add(-time.Minute), PeriodEnd: now, Samples: []domain.ResourceUsageSample{}}
	namespaces, err := s.ListNamespaces(ctx)
	if err != nil {
		return b, err
	}
	type point struct {
		pod    finOpsPod
		metric finOpsMetric
		env    string
	}
	var points []point
	b.MetricsAvailable = true
	for _, ns := range namespaces {
		if ns.Metadata.Labels["envplane.io/project-id"] != project {
			continue
		}
		env := ns.Metadata.Labels[environmentIDLabel]
		if env == "" {
			b.UnattributedPods++
			continue
		}
		var pods []finOpsPod
		err = s.listPages(ctx, s.apiURL+"/api/v1/namespaces/"+url.PathEscape(ns.Metadata.Name)+"/pods", "FinOps pods", func(raw json.RawMessage) error {
			var p finOpsPod
			if e := json.Unmarshal(raw, &p); e != nil {
				return e
			}
			pods = append(pods, p)
			return nil
		})
		if err != nil {
			return b, err
		}
		b.ExpectedPods += len(pods)
		metrics := map[string]finOpsMetric{}
		err = s.listPages(ctx, s.apiURL+"/apis/metrics.k8s.io/v1beta1/namespaces/"+url.PathEscape(ns.Metadata.Name)+"/pods", "FinOps metrics", func(raw json.RawMessage) error {
			var m finOpsMetric
			if e := json.Unmarshal(raw, &m); e != nil {
				return e
			}
			metrics[m.Metadata.Name] = m
			return nil
		})
		if err != nil {
			b.MetricsAvailable = false
			continue
		}
		for _, p := range pods {
			component := p.Metadata.Labels["envplane.io/component"]
			if component == "" {
				component = p.Metadata.Labels["app.kubernetes.io/component"]
			}
			if component == "" || p.Metadata.UID == "" || (p.Metadata.Labels[environmentIDLabel] != "" && p.Metadata.Labels[environmentIDLabel] != env) {
				b.UnattributedPods++
				continue
			}
			m, ok := metrics[p.Metadata.Name]
			if !ok {
				continue
			}
			window, e := time.ParseDuration(m.Window)
			if e != nil || window <= 0 || window > 5*time.Minute || m.Timestamp.After(now.Add(time.Minute)) || m.Timestamp.Before(now.Add(-5*time.Minute)) || len(m.Containers) == 0 {
				continue
			}
			mstart := m.Timestamp.Add(-window)
			if p.Metadata.CreatedAt.IsZero() || mstart.Before(p.Metadata.CreatedAt) {
				continue
			}
			if len(points) == 0 {
				b.PeriodStart = mstart
				b.PeriodEnd = m.Timestamp
			} else {
				if mstart.After(b.PeriodStart) {
					b.PeriodStart = mstart
				}
				if m.Timestamp.Before(b.PeriodEnd) {
					b.PeriodEnd = m.Timestamp
				}
			}
			points = append(points, point{p, m, env})
		}
	}
	if !b.MetricsAvailable || !b.PeriodEnd.After(b.PeriodStart) {
		b.MetricsAvailable = false
		points = nil
		b.PeriodStart = now.Add(-time.Minute)
		b.PeriodEnd = now
	}
	for _, p := range points {
		cpu, memory := 0.0, 0.0
		valid := true
		expected := map[string]bool{}
		for _, container := range p.pod.Spec.Containers {
			expected[container.Name] = true
		}
		if len(expected) == 0 || len(expected) != len(p.metric.Containers) {
			valid = false
		}
		for _, container := range p.metric.Containers {
			if !expected[container.Name] {
				valid = false
				break
			}
			delete(expected, container.Name)
			c, e := finOpsQuantity(container.Usage["cpu"])
			if e != nil {
				valid = false
				break
			}
			m, e := finOpsQuantity(container.Usage["memory"])
			if e != nil {
				valid = false
				break
			}
			cpu += c
			memory += m
		}
		if !valid {
			continue
		}
		component := p.pod.Metadata.Labels["envplane.io/component"]
		if component == "" {
			component = p.pod.Metadata.Labels["app.kubernetes.io/component"]
		}
		id := sha256.Sum256([]byte(cluster + "|" + p.pod.Metadata.UID + "|" + b.PeriodStart.Format(time.RFC3339Nano) + "|" + b.PeriodEnd.Format(time.RFC3339Nano)))
		b.Samples = append(b.Samples, domain.ResourceUsageSample{SnapshotID: hex.EncodeToString(id[:]), ProjectID: project, ClusterID: cluster, EnvironmentID: p.env, ComponentID: component, MeasurementKind: domain.FinOpsMeasured, Source: "kubernetes-metrics-api", CPUCoreHours: cpu * b.PeriodEnd.Sub(b.PeriodStart).Hours(), MemoryGiBHours: memory / (1 << 30) * b.PeriodEnd.Sub(b.PeriodStart).Hours(), OccurredAt: b.PeriodEnd, PeriodStart: b.PeriodStart, PeriodEnd: b.PeriodEnd})
		b.MeasuredPods++
	}
	id := sha256.Sum256([]byte(project + "|" + cluster + "|" + agentID + "|" + b.PeriodStart.Format(time.RFC3339Nano) + "|" + b.PeriodEnd.Format(time.RFC3339Nano)))
	b.BatchID = hex.EncodeToString(id[:])
	return b, nil
}

func finOpsQuantity(value string) (float64, error) {
	units := []struct {
		suffix string
		factor float64
	}{{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40}, {"n", 1e-9}, {"u", 1e-6}, {"m", 1e-3}, {"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"", 1}}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			v, err := strconv.ParseFloat(strings.TrimSuffix(value, unit.suffix), 64)
			v *= unit.factor
			if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				return 0, errors.New("invalid metrics quantity")
			}
			return v, nil
		}
	}
	return 0, errors.New("unsupported metrics quantity")
}

// SubmitFinOps accepts only TLS endpoints and never logs bearer or payload.
func SubmitFinOps(ctx context.Context, client *http.Client, base, token string, b domain.FinOpsMeteringBatch) error {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || token == "" || client == nil {
		return errors.New("secure FinOps transport required")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/agents/finops/metering"
	u.RawQuery = ""
	u.Fragment = ""
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	bounded := *client
	bounded.Timeout = 15 * time.Second
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := bounded.Do(req)
	if err != nil {
		return &FinOpsDeliveryError{}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusNoContent {
		return &FinOpsDeliveryError{StatusCode: resp.StatusCode}
	}
	return nil
}
