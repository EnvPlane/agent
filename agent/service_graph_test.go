package agent

import (
	"encoding/json"
	"testing"

	"github.com/envplane/contracts/domain"
)

func TestBuildServiceGraphMapsServicesIngressAndEnvDependencies(t *testing.T) {
	graph := BuildServiceGraph([]domain.ResourceSnapshot{
		{
			Kind:      "Service",
			Namespace: "dev-base",
			Name:      "orders",
			Selector:  map[string]string{"app": "orders"},
		},
		{
			Kind:      "Service",
			Namespace: "dev-base",
			Name:      "payments",
			Selector:  map[string]string{"app": "payments"},
		},
		{
			Kind:      "Deployment",
			Namespace: "dev-base",
			Name:      "orders",
			PodLabels: map[string]string{"app": "orders"},
			EnvVars: []domain.ResourceEnvVar{
				{Name: "PAYMENTS_URL", Value: "http://payments.dev-base.svc.cluster.local:8080"},
			},
		},
		{
			Kind:      "Ingress",
			Namespace: "dev-base",
			Name:      "orders-public",
			IngressRules: []domain.ResourceIngressRule{
				{Host: "preview.example.com", Path: "/orders", ServiceName: "orders", ServicePort: "80"},
			},
		},
	})

	assertEdge(t, graph, "Service/dev-base/orders", "Deployment/dev-base/orders", "selects", 1)
	assertEdge(t, graph, "Ingress/dev-base/orders-public", "Service/dev-base/orders", "routes-to", 1)
	assertEdge(t, graph, "Deployment/dev-base/orders", "Service/dev-base/payments", "depends-on", 0.95)
}

func TestBuildServiceGraphCMSLikeMultiNamespaceGolden(t *testing.T) {
	snapshots := []domain.ResourceSnapshot{
		{Kind: "Service", Namespace: "dev-cms", Name: "web", Selector: map[string]string{"app": "web"}},
		{Kind: "Deployment", Namespace: "dev-cms", Name: "web", PodLabels: map[string]string{"app": "web"}, Manifest: map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"serviceAccountName": "web", "volumes": []any{map[string]any{"configMap": map[string]any{"name": "web-config"}}}}}}}},
		{Kind: "ServiceAccount", Namespace: "dev-cms", Name: "web"}, {Kind: "ConfigMap", Namespace: "dev-cms", Name: "web-config"},
		{Kind: "Service", Namespace: "dev-cms-content", Name: "web", Selector: map[string]string{"app": "web"}},
		{Kind: "Deployment", Namespace: "dev-cms-content", Name: "web", PodLabels: map[string]string{"app": "web"}},
	}
	graph := BuildServiceGraph(snapshots)
	if graph.Validation == nil || !graph.Validation.Valid { t.Fatalf("CMS graph should be valid: %#v", graph.Validation) }
	assertEdge(t, graph, "Deployment/dev-cms/web", "ConfigMap/dev-cms/web-config", "references", 1)
	assertEdge(t, graph, "Deployment/dev-cms/web", "ServiceAccount/dev-cms/web", "references", 1)
	if _, ok := findGraphNode(graph, "Service/dev-cms-content/web"); !ok { t.Fatal("same-name resource from second namespace was lost") }
	encoded, err := json.Marshal(graph); if err != nil { t.Fatal(err) }; if len(encoded) == 0 { t.Fatal("empty graph golden") }
}

func findGraphNode(graph domain.ServiceGraph, id string) (domain.ServiceGraphNode, bool) { for _, node := range graph.Nodes { if node.ID == id { return node, true } }; return domain.ServiceGraphNode{}, false }

func TestBuildServiceGraphMarksAmbiguousEnvDependenciesWithConfidence(t *testing.T) {
	graph := BuildServiceGraph([]domain.ResourceSnapshot{
		{
			Kind:      "Service",
			Namespace: "dev-base",
			Name:      "redis",
		},
		{
			Kind:      "Deployment",
			Namespace: "dev-base",
			Name:      "worker",
			EnvVars: []domain.ResourceEnvVar{
				{Name: "REDIS_HOST"},
			},
			EnvFrom: []domain.ResourceEnvFromRef{
				{Kind: "ConfigMap", Name: "redis-client-config"},
			},
		},
	})

	assertEdge(t, graph, "Deployment/dev-base/worker", "Service/dev-base/redis", "depends-on", 0.7)
}

func TestBuildServiceGraphUsesOwnerReferences(t *testing.T) {
	graph := BuildServiceGraph([]domain.ResourceSnapshot{
		{
			Kind:      "ReplicaSet",
			Namespace: "dev-base",
			Name:      "orders-abc",
			OwnerReferences: []domain.ResourceOwnerReference{
				{Kind: "Deployment", Name: "orders"},
			},
		},
		{
			Kind:      "Deployment",
			Namespace: "dev-base",
			Name:      "orders",
		},
	})

	assertEdge(t, graph, "ReplicaSet/dev-base/orders-abc", "Deployment/dev-base/orders", "owned-by", 1)
}

func assertEdge(t *testing.T, graph domain.ServiceGraph, from string, to string, edgeType string, minConfidence float64) {
	t.Helper()
	for _, edge := range graph.Edges {
		if edge.From == from && edge.To == to && edge.Type == edgeType {
			if edge.Confidence < minConfidence {
				t.Fatalf("edge %s -> %s type=%s confidence=%f, want >= %f", from, to, edgeType, edge.Confidence, minConfidence)
			}
			return
		}
	}
	t.Fatalf("edge %s -> %s type=%s missing; edges=%#v", from, to, edgeType, graph.Edges)
}
