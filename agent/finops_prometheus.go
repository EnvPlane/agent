package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/envplane/contracts/domain"
)

type FinOpsPrometheusSource struct {
	endpoint string
	client   *http.Client
}

// NewFinOpsPrometheusSource accepts an explicitly approved exact HTTPS origin,
// not arbitrary Agent/UI URLs. Redirects, credentials and caller PromQL are not
// accepted. Source authentication belongs to the operator-supplied transport.
func NewFinOpsPrometheusSource(endpoint string, allowedOrigins []string, client *http.Client) (*FinOpsPrometheusSource, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || client == nil {
		return nil, errors.New("approved secure metrics origin required")
	}
	approved := false
	for _, origin := range allowedOrigins {
		if origin == "https://"+u.Host {
			approved = true
		}
	}
	if !approved {
		return nil, errors.New("metrics origin not approved")
	}
	copyClient := *client
	copyClient.Timeout = 15 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &FinOpsPrometheusSource{endpoint: "https://" + u.Host, client: &copyClient}, nil
}

type finOpsPromSeries struct {
	Metric map[string]string   `json:"metric"`
	Values [][]json.RawMessage `json:"values"`
}
type finOpsPromResponse struct {
	Status   string   `json:"status"`
	Warnings []string `json:"warnings"`
	Data     struct {
		ResultType string             `json:"resultType"`
		Result     []finOpsPromSeries `json:"result"`
	} `json:"data"`
}

func prometheusMetric(d domain.FinOpsDimension) string {
	switch d {
	case domain.FinOpsNetworkTransmit:
		return "container_network_transmit_bytes_total"
	case domain.FinOpsNetworkReceive:
		return "container_network_receive_bytes_total"
	case domain.FinOpsGPUUtilization:
		return "DCGM_FI_DEV_GPU_UTIL"
	default:
		return ""
	}
}

func (s *FinOpsPrometheusSource) Collect(ctx context.Context, d domain.FinOpsDimension, owned []FinOpsOwnedResource, start, end time.Time) (domain.FinOpsDimensionReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r := dimensionReport(d, start, end)
	metric := prometheusMetric(d)
	if s == nil || metric == "" || !end.After(start) || end.Sub(start) > 5*time.Minute {
		return r, errors.New("invalid dimension window")
	}
	byUID := map[string]FinOpsOwnedResource{}
	for _, o := range owned {
		if o.Namespace == "" || o.ResourceUID == "" || o.EnvironmentID == "" || o.ComponentID == "" || o.PodName == "" || (d == domain.FinOpsGPUUtilization && o.ExpectedGPUs <= 0) {
			continue
		}
		if _, exists := byUID[o.ResourceUID]; exists {
			return r, errors.New("ambiguous ownership")
		}
		byUID[o.ResourceUID] = o
	}
	r.ExpectedResources = len(byUID)
	if r.ExpectedResources > 1000 {
		return r, errors.New("metrics inventory bound exceeded")
	}
	if len(byUID) == 0 {
		r.Reason = "no-explicit-owned-inventory"
		return r, nil
	}
	// Query only the explicit namespace/pod/UID inventory. Missing pod_uid labels
	// remain unavailable, rather than attributing recycled pod names to owners.
	var all []finOpsPromSeries
	for _, o := range byUID {
		selector := metric + "{namespace=" + strconv.Quote(o.Namespace) + ",pod=" + strconv.Quote(o.PodName) + ",pod_uid=" + strconv.Quote(o.ResourceUID) + "}"
		if d != domain.FinOpsGPUUtilization {
			// Endpoint delta alone misses resets which recover above the old
			// value. Require a reset-free observed Prometheus counter window.
			q := url.Values{"query": {"resets(" + selector + "[" + strconv.Itoa(int(math.Ceil(end.Sub(start).Seconds()))) + "s])"}, "time": {strconv.FormatFloat(float64(end.UnixNano())/1e9, 'f', 9, 64)}}
			req, e := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"/api/v1/query?"+q.Encode(), nil)
			if e != nil {
				return r, e
			}
			response, e := s.client.Do(req)
			if e != nil {
				return r, errors.New("reset coverage unavailable")
			}
			var reset struct {
				Status   string   `json:"status"`
				Warnings []string `json:"warnings"`
				Data     struct {
					ResultType string `json:"resultType"`
					Result     []struct {
						Metric map[string]string `json:"metric"`
						Value  []json.RawMessage `json:"value"`
					} `json:"result"`
				} `json:"data"`
			}
			e = json.NewDecoder(http.MaxBytesReader(nil, response.Body, 2<<20)).Decode(&reset)
			_ = response.Body.Close()
			if e != nil || response.StatusCode != http.StatusOK || reset.Status != "success" || reset.Data.ResultType != "vector" || len(reset.Warnings) > 0 || len(reset.Data.Result) == 0 || len(reset.Data.Result) > 1000 {
				return r, errors.New("reset coverage unavailable")
			}
			for _, sample := range reset.Data.Result {
				if sample.Metric["pod_uid"] != o.ResourceUID || sample.Metric["namespace"] != o.Namespace || sample.Metric["pod"] != o.PodName || len(sample.Value) != 2 {
					return r, errors.New("reset attribution unavailable")
				}
				var value string
				var timestamp float64
				if json.Unmarshal(sample.Value[1], &value) != nil || json.Unmarshal(sample.Value[0], &timestamp) != nil || math.Abs(timestamp-float64(end.UnixNano())/1e9) > 0.001 {
					return r, errors.New("reset coverage gap")
				}
				count, e := strconv.ParseFloat(value, 64)
				if e != nil || count != 0 {
					return r, errors.New("counter reset or unknown reset count")
				}
			}
		}
		q := url.Values{"query": {selector}, "start": {strconv.FormatFloat(float64(start.UnixNano())/1e9, 'f', 9, 64)}, "end": {strconv.FormatFloat(float64(end.UnixNano())/1e9, 'f', 9, 64)}, "step": {strconv.FormatFloat(end.Sub(start).Seconds(), 'f', 9, 64)}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"/api/v1/query_range?"+q.Encode(), nil)
		if err != nil {
			return r, err
		}
		response, err := s.client.Do(req)
		if err != nil {
			return r, errors.New("metrics request unavailable")
		}
		var decoded finOpsPromResponse
		err = json.NewDecoder(http.MaxBytesReader(nil, response.Body, 2<<20)).Decode(&decoded)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || decoded.Status != "success" || decoded.Data.ResultType != "matrix" || len(decoded.Warnings) > 0 || len(decoded.Data.Result) > 1000 {
			return r, errors.New("incomplete metrics response")
		}
		all = append(all, decoded.Data.Result...)
	}
	totals := map[string]float64{}
	counts := map[string]int{}
	bad := map[string]bool{}
	seriesIDs := map[string]bool{}
	for _, series := range all {
		uid := series.Metric["pod_uid"]
		owner, ok := byUID[uid]
		if !ok || series.Metric["__name__"] != metric || series.Metric["namespace"] != owner.Namespace || series.Metric["pod"] != owner.PodName {
			return r, errors.New("untrusted metrics attribution")
		}
		device := series.Metric["interface"]
		if d == domain.FinOpsGPUUtilization {
			device = series.Metric["UUID"]
		} else if device == "lo" {
			continue
		}
		if device == "" || seriesIDs[uid+"|"+device] {
			bad[uid] = true
			continue
		}
		seriesIDs[uid+"|"+device] = true
		first, last, err := prometheusWindow(series.Values, start, end)
		if err != nil {
			bad[uid] = true
			continue
		}
		quantity := (last - first) / (1 << 30)
		if d == domain.FinOpsGPUUtilization {
			if first > 100 || last > 100 {
				bad[uid] = true
				continue
			}
			quantity = (first + last) / 200 * end.Sub(start).Hours()
		} else if last < first {
			bad[uid] = true
			continue
		} // reset: missing counter interval, never invent delta
		totals[uid] += quantity
		counts[uid]++
	}
	for uid, owner := range byUID {
		if bad[uid] || counts[uid] == 0 || (d == domain.FinOpsGPUUtilization && counts[uid] != owner.ExpectedGPUs) {
			continue
		}
		id := sha256.Sum256([]byte(string(d) + "|" + uid + "|" + start.Format(time.RFC3339Nano) + "|" + end.Format(time.RFC3339Nano)))
		r.Samples = append(r.Samples, domain.FinOpsDimensionSample{SampleID: hex.EncodeToString(id[:]), EnvironmentID: owner.EnvironmentID, ComponentID: owner.ComponentID, Namespace: owner.Namespace, ResourceUID: uid, Quantity: totals[uid]})
		r.ObservedResources++
	}
	r.Reason = "missing-series-reset-or-device-coverage"
	if r.ObservedResources == r.ExpectedResources {
		r.State = "complete"
		r.Reason = ""
	} else if r.ObservedResources > 0 {
		r.State = "partial"
	}
	return r, nil
}

func prometheusWindow(values [][]json.RawMessage, start, end time.Time) (float64, float64, error) {
	if len(values) != 2 {
		return 0, 0, errors.New("exact boundary samples required")
	}
	parse := func(raw []json.RawMessage, expected time.Time) (float64, error) {
		if len(raw) != 2 {
			return 0, errors.New("invalid sample")
		}
		var timestamp float64
		var number string
		if json.Unmarshal(raw[0], &timestamp) != nil || json.Unmarshal(raw[1], &number) != nil || math.Abs(timestamp-float64(expected.UnixNano())/1e9) > 0.001 {
			return 0, errors.New("metrics gap")
		}
		v, err := strconv.ParseFloat(number, 64)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("invalid metrics unit value")
		}
		return v, nil
	}
	first, err := parse(values[0], start)
	if err != nil {
		return 0, 0, err
	}
	last, err := parse(values[1], end)
	return first, last, err
}
