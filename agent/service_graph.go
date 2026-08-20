package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/envplane/contracts/domain"
)

func BuildServiceGraph(snapshots []domain.ResourceSnapshot) domain.ServiceGraph {
	nodes := make(map[string]domain.ServiceGraphNode)
	services := make([]domain.ResourceSnapshot, 0)
	workloads := make([]domain.ResourceSnapshot, 0)
	ingresses := make([]domain.ResourceSnapshot, 0)

	byKindNamespaceName := make(map[string]domain.ResourceSnapshot, len(snapshots))
	duplicateIDs := make([]string, 0)
	servicesByNamespace := make(map[string][]domain.ResourceSnapshot)

	for _, snapshot := range snapshots {
		if snapshot.Kind == "" || snapshot.Name == "" || snapshot.Namespace == "" {
			continue
		}
		id := serviceGraphNodeID(snapshot.Kind, snapshot.Namespace, snapshot.Name)
		if _, exists := byKindNamespaceName[id]; exists { duplicateIDs = append(duplicateIDs, id) }
		nodes[id] = domain.ServiceGraphNode{
			ID:        id,
			Kind:      snapshot.Kind,
			Namespace: snapshot.Namespace,
			Name:      snapshot.Name,
			Labels:    snapshot.Labels,
		}
		byKindNamespaceName[id] = snapshot
		switch snapshot.Kind {
		case "Service":
			services = append(services, snapshot)
			servicesByNamespace[snapshot.Namespace] = append(servicesByNamespace[snapshot.Namespace], snapshot)
		case "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob":
			workloads = append(workloads, snapshot)
		case "Ingress":
			ingresses = append(ingresses, snapshot)
		}
	}

	edgeSet := make(map[string]domain.ServiceGraphEdge)
	addEdge := func(edge domain.ServiceGraphEdge) {
		if edge.From == "" || edge.To == "" || edge.Type == "" {
			return
		}
		if edge.Confidence <= 0 {
			edge.Confidence = 0.5
		}
		key := edge.From + "\x00" + edge.To + "\x00" + edge.Type + "\x00" + edge.Reason
		if existing, ok := edgeSet[key]; ok && existing.Confidence >= edge.Confidence {
			return
		}
		edgeSet[key] = edge
	}

	for _, ingress := range ingresses {
		from := serviceGraphNodeID(ingress.Kind, ingress.Namespace, ingress.Name)
		for _, rule := range ingress.IngressRules {
			target, ok := findServiceByName(servicesByNamespace[ingress.Namespace], rule.ServiceName)
			if !ok {
				continue
			}
			addEdge(domain.ServiceGraphEdge{
				From:       from,
				To:         serviceGraphNodeID(target.Kind, target.Namespace, target.Name),
				Type:       "routes-to",
				Reason:     ingressRouteReason(rule),
				Confidence: 1,
			})
		}
	}

	for _, service := range services {
		if len(service.Selector) == 0 {
			continue
		}
		from := serviceGraphNodeID(service.Kind, service.Namespace, service.Name)
		for _, workload := range workloads {
			if service.Namespace != workload.Namespace {
				continue
			}
			if selectorMatchesLabels(service.Selector, firstNonEmptyMap(workload.PodLabels, workload.Labels)) {
				addEdge(domain.ServiceGraphEdge{
					From:       from,
					To:         serviceGraphNodeID(workload.Kind, workload.Namespace, workload.Name),
					Type:       "selects",
					Reason:     "service selector matches workload labels",
					Confidence: 1,
				})
			}
		}
	}

	for _, snapshot := range snapshots {
		from := serviceGraphNodeID(snapshot.Kind, snapshot.Namespace, snapshot.Name)
		for _, owner := range snapshot.OwnerReferences {
			targetID := serviceGraphNodeID(owner.Kind, snapshot.Namespace, owner.Name)
			if _, ok := byKindNamespaceName[targetID]; !ok {
				continue
			}
			addEdge(domain.ServiceGraphEdge{
				From:       from,
				To:         targetID,
				Type:       "owned-by",
				Reason:     "ownerReference",
				Confidence: 1,
			})
		}
	}

	for _, workload := range workloads {
		from := serviceGraphNodeID(workload.Kind, workload.Namespace, workload.Name)
		for _, service := range servicesByNamespace[workload.Namespace] {
			confidence, reason := inferDependencyConfidence(workload, service)
			if confidence <= 0 {
				continue
			}
			addEdge(domain.ServiceGraphEdge{
				From:       from,
				To:         serviceGraphNodeID(service.Kind, service.Namespace, service.Name),
				Type:       "depends-on",
				Reason:     reason,
				Confidence: confidence,
			})
		}
	}

	for _, snapshot := range snapshots {
		from := serviceGraphNodeID(snapshot.Kind, snapshot.Namespace, snapshot.Name)
		addManifestReferenceEdges(snapshot, byKindNamespaceName, addEdge, from)
		addServiceDNSReferences(snapshot, byKindNamespaceName, addEdge, from)
		if snapshot.SourceMapping != nil && snapshot.SourceMapping.Status == "resolved" {
			provenance := serviceGraphNodeID(snapshot.SourceMapping.Kind, snapshot.SourceMapping.Namespace, snapshot.SourceMapping.Name)
			if _, ok := byKindNamespaceName[provenance]; ok { addEdge(domain.ServiceGraphEdge{From: from, To: provenance, Type: "provenance", Reason: "source mapping", Confidence: 1}) }
		}
	}

	graphNodes := make([]domain.ServiceGraphNode, 0, len(nodes))
	for _, node := range nodes {
		graphNodes = append(graphNodes, node)
	}
	sort.Slice(graphNodes, func(i, j int) bool { return graphNodes[i].ID < graphNodes[j].ID })

	graphEdges := make([]domain.ServiceGraphEdge, 0, len(edgeSet))
	for _, edge := range edgeSet {
		graphEdges = append(graphEdges, edge)
	}
	sort.Slice(graphEdges, func(i, j int) bool {
		if graphEdges[i].From != graphEdges[j].From {
			return graphEdges[i].From < graphEdges[j].From
		}
		if graphEdges[i].To != graphEdges[j].To {
			return graphEdges[i].To < graphEdges[j].To
		}
		if graphEdges[i].Type != graphEdges[j].Type {
			return graphEdges[i].Type < graphEdges[j].Type
		}
		return graphEdges[i].Reason < graphEdges[j].Reason
	})

	graph := domain.ServiceGraph{Nodes: graphNodes, Edges: graphEdges}
	graph.Policies = buildResourceDependencyPolicies(snapshots)
	graph.Validation = validateDependencyGraph(graph, duplicateIDs)
	return graph
}

func addManifestReferenceEdges(snapshot domain.ResourceSnapshot, nodes map[string]domain.ResourceSnapshot, add func(domain.ServiceGraphEdge), from string) {
	var walk func(any, string)
	walk = func(value any, path string) {
		switch item := value.(type) {
		case map[string]any:
			for key, child := range item {
				childPath := path + "." + key
				if name, ok := child.(string); ok && strings.TrimSpace(name) != "" {
					kind := ""
					switch key { case "serviceAccountName": kind = "ServiceAccount"; case "claimName", "persistentVolumeClaim": kind = "PersistentVolumeClaim"; case "configMap", "configMapKeyRef": kind = "ConfigMap"; case "secret", "secretKeyRef", "secretName": kind = "Secret" }
					if kind != "" { addReferenceEdge(snapshot, nodes, add, from, kind, name, childPath) }
				}
				if ref, ok := child.(map[string]any); ok {
					kind, name := strings.TrimSpace(stringifyGraphValue(ref["kind"])), strings.TrimSpace(stringifyGraphValue(ref["name"]))
					if kind == "" { switch key { case "configMap", "configMapKeyRef": kind = "ConfigMap"; case "secret", "secretKeyRef": kind = "Secret"; case "persistentVolumeClaim": kind = "PersistentVolumeClaim" } }
					if name == "" { name = strings.TrimSpace(stringifyGraphValue(ref["name"])) }
					if kind != "" && name != "" { addReferenceEdge(snapshot, nodes, add, from, kind, name, childPath) }
				}
				walk(child, childPath)
			}
		case []any: for index, child := range item { walk(child, fmt.Sprintf("%s[%d]", path, index)) }
		}
	}
	if snapshot.Manifest != nil { walk(snapshot.Manifest, "manifest") }
	for _, env := range snapshot.EnvFrom { addReferenceEdge(snapshot, nodes, add, from, env.Kind, env.Name, "envFrom."+env.Kind) }
}

func addReferenceEdge(snapshot domain.ResourceSnapshot, nodes map[string]domain.ResourceSnapshot, add func(domain.ServiceGraphEdge), from, kind, name, path string) {
	id := serviceGraphNodeID(kind, snapshot.Namespace, name); required := true
	if _, ok := nodes[id]; !ok { add(domain.ServiceGraphEdge{From: from, To: id, Type: "references", Reason: "required resource reference", Confidence: 1, Required: required, Path: path}); return }
	add(domain.ServiceGraphEdge{From: from, To: id, Type: "references", Reason: "manifest reference", Confidence: 1, Required: required, Path: path})
}

func stringifyGraphValue(value any) string { if text, ok := value.(string); ok { return text }; return "" }

func addServiceDNSReferences(snapshot domain.ResourceSnapshot, nodes map[string]domain.ResourceSnapshot, add func(domain.ServiceGraphEdge), from string) {
	for _, env := range snapshot.EnvVars {
		value := strings.TrimSpace(strings.ToLower(env.Value)); if value == "" { continue }
		if at := strings.LastIndex(value, "@"); at >= 0 { value = value[at+1:] }; value = strings.TrimPrefix(value, "http://"); value = strings.TrimPrefix(value, "https://"); value = strings.Split(value, "/")[0]; value = strings.Split(value, ":")[0]
		parts := strings.Split(value, "."); if len(parts) < 2 || parts[0] == "" || parts[1] == "" { continue }
		id := serviceGraphNodeID("Service", parts[1], parts[0]); required := true; reason := "service DNS/URL/DSN reference"
		if _, ok := nodes[id]; !ok { add(domain.ServiceGraphEdge{From: from, To: id, Type: "dns-depends-on", Reason: reason, Confidence: 0.9, Required: required, Path: "env."+env.Name}); continue }
		add(domain.ServiceGraphEdge{From: from, To: id, Type: "dns-depends-on", Reason: reason, Confidence: 1, Required: required, Path: "env."+env.Name})
	}
}

func buildResourceDependencyPolicies(snapshots []domain.ResourceSnapshot) []domain.ResourceDependencyPolicy {
	policies := make([]domain.ResourceDependencyPolicy, 0, len(snapshots)); for _, snapshot := range snapshots {
		strategy, reason, required := domain.ResourcePolicyClone, "workload-owned desired state defaults to clone", false
		switch snapshot.Kind { case "Secret": strategy, reason, required = domain.ResourcePolicyUnsupported, "Secret materialization is deferred to EP-TPL-005", true; case "PersistentVolumeClaim": strategy, reason, required = domain.ResourcePolicyUnsupported, "PVC materialization is deferred to EP-TPL-006", true; case "Pod", "ReplicaSet", "ControllerRevision", "Endpoint", "EndpointSlice", "Event", "Lease": strategy, reason = domain.ResourcePolicyIgnore, "runtime child is never part of desired state"; case "Service", "Ingress", "ConfigMap", "ServiceAccount", "ResourceQuota", "LimitRange", "NetworkPolicy", "HorizontalPodAutoscaler", "PodDisruptionBudget": strategy, reason = domain.ResourcePolicyClone, "selected desired-state resource defaults to clone" }
		policies = append(policies, domain.ResourceDependencyPolicy{ResourceID: serviceGraphNodeID(snapshot.Kind, snapshot.Namespace, snapshot.Name), Kind: snapshot.Kind, Namespace: snapshot.Namespace, Name: snapshot.Name, Strategy: strategy, Defaulted: true, Reason: reason, Required: required})
	}; sort.Slice(policies, func(i,j int) bool { return policies[i].ResourceID < policies[j].ResourceID }); return policies
}

func validateDependencyGraph(graph domain.ServiceGraph, duplicates []string) *domain.DependencyGraphValidation {
	result := &domain.DependencyGraphValidation{Valid: true}; nodeSet := map[string]bool{}; for _, node := range graph.Nodes { nodeSet[node.ID] = true }
	for _, id := range duplicates { result.Errors = append(result.Errors, domain.DependencyGraphIssue{Code: "duplicate_resource", ResourceID: id, Message: "resource identity is duplicated; namespace-qualified identity is required"}) }
	for _, edge := range graph.Edges { if edge.Required && !nodeSet[edge.To] { result.Errors = append(result.Errors, domain.DependencyGraphIssue{Code: "dangling_required_reference", ResourceID: edge.From, Path: edge.Path, Message: fmt.Sprintf("required dependency %s is missing", edge.To)}) } }
	for _, policy := range graph.Policies { if policy.Defaulted && policy.Strategy == domain.ResourcePolicyUnsupported { result.Errors = append(result.Errors, domain.DependencyGraphIssue{Code: "strategy_required", ResourceID: policy.ResourceID, Message: policy.Reason}) } }
	for _, edge := range graph.Edges { if edge.Type == "selects" { count := 0; for _, candidate := range graph.Edges { if candidate.Type == "selects" && candidate.From == edge.From { count++ } }; if count > 1 { result.Errors = append(result.Errors, domain.DependencyGraphIssue{Code: "ambiguous_selector", ResourceID: edge.From, Message: "selector matches multiple workloads"}); break } } }
	adjacency := map[string][]string{}; for _, edge := range graph.Edges { if edge.Type == "references" || edge.Type == "depends-on" || edge.Type == "dns-depends-on" { adjacency[edge.From] = append(adjacency[edge.From], edge.To) } }
	visiting, visited := map[string]bool{}, map[string]bool{}; var visit func(string) bool; visit = func(id string) bool { if visiting[id] { return true }; if visited[id] { return false }; visiting[id] = true; for _, next := range adjacency[id] { if visit(next) { return true } }; delete(visiting, id); visited[id] = true; return false }; for id := range adjacency { if visit(id) { result.Errors = append(result.Errors, domain.DependencyGraphIssue{Code: "dependency_cycle", ResourceID: id, Message: "dependency graph contains a cycle"}); break } }
	if len(result.Errors) > 0 { result.Valid = false }; return result
}

func serviceGraphNodeID(kind string, namespace string, name string) string {
	return strings.TrimSpace(kind) + "/" + strings.TrimSpace(namespace) + "/" + strings.TrimSpace(name)
}

func selectorMatchesLabels(selector map[string]string, labels map[string]string) bool {
	if len(selector) == 0 || len(labels) == 0 {
		return false
	}
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func firstNonEmptyMap(items ...map[string]string) map[string]string {
	for _, item := range items {
		if len(item) > 0 {
			return item
		}
	}
	return nil
}

func findServiceByName(services []domain.ResourceSnapshot, name string) (domain.ResourceSnapshot, bool) {
	name = strings.TrimSpace(name)
	for _, service := range services {
		if service.Name == name {
			return service, true
		}
	}
	return domain.ResourceSnapshot{}, false
}

func ingressRouteReason(rule domain.ResourceIngressRule) string {
	parts := make([]string, 0, 3)
	if strings.TrimSpace(rule.Host) != "" {
		parts = append(parts, "host="+strings.TrimSpace(rule.Host))
	}
	if strings.TrimSpace(rule.Path) != "" {
		parts = append(parts, "path="+strings.TrimSpace(rule.Path))
	}
	if strings.TrimSpace(rule.ServicePort) != "" {
		parts = append(parts, "port="+strings.TrimSpace(rule.ServicePort))
	}
	if len(parts) == 0 {
		return "ingress backend service"
	}
	return "ingress route " + strings.Join(parts, " ")
}

func inferDependencyConfidence(workload domain.ResourceSnapshot, service domain.ResourceSnapshot) (float64, string) {
	serviceName := strings.ToLower(strings.TrimSpace(service.Name))
	if serviceName == "" || strings.EqualFold(workload.Name, service.Name) {
		return 0, ""
	}
	for _, env := range workload.EnvVars {
		name := strings.ToLower(strings.TrimSpace(env.Name))
		value := strings.ToLower(strings.TrimSpace(env.Value))
		if envValueReferencesService(value, serviceName, service.Namespace) {
			return 0.95, "env var value references service name"
		}
		if tokenContainsServiceName(name, serviceName) {
			return 0.7, "env var name references service name"
		}
	}
	for _, ref := range workload.EnvFrom {
		refName := strings.ToLower(strings.TrimSpace(ref.Name))
		if tokenContainsServiceName(refName, serviceName) {
			return 0.55, "envFrom reference name resembles service name"
		}
	}
	return 0, ""
}

func envValueReferencesService(value string, serviceName string, namespace string) bool {
	if value == "" {
		return false
	}
	candidates := []string{
		serviceName,
		serviceName + "." + strings.ToLower(strings.TrimSpace(namespace)),
		serviceName + "." + strings.ToLower(strings.TrimSpace(namespace)) + ".svc",
	}
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func tokenContainsServiceName(token string, serviceName string) bool {
	if token == "" || serviceName == "" {
		return false
	}
	normalizedToken := strings.NewReplacer("_", "-", ".", "-", "/", "-").Replace(token)
	normalizedService := strings.NewReplacer("_", "-", ".", "-", "/", "-").Replace(serviceName)
	return strings.Contains(normalizedToken, normalizedService)
}
