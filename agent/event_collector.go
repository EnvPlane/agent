package agent

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/envplane/contracts/domain"
)

type EventCollector struct {
	source  EventSource
	limit   int
	mu      sync.Mutex
	sent    map[string]map[string]struct{}
	pending map[string]map[string]struct{}
}

func NewEventCollector(source EventSource) *EventCollector {
	return &EventCollector{source: source, limit: 50, sent: make(map[string]map[string]struct{}), pending: make(map[string]map[string]struct{})}
}

func (c *EventCollector) Collect(ctx context.Context, namespace string) ([]domain.KubernetesEvent, error) {
	events, err := c.source.ListEvents(ctx, namespace)
	if err != nil {
		return nil, err
	}
	items := BuildEnvironmentEvents(events, c.limit)
	c.mu.Lock()
	defer c.mu.Unlock()
	unsent := make([]domain.KubernetesEvent, 0, len(items))
	for _, item := range items {
		key := eventKey(item)
		if _, ok := c.sent[namespace][key]; ok {
			continue
		}
		if _, ok := c.pending[namespace][key]; ok {
			continue
		}
		if c.pending[namespace] == nil {
			c.pending[namespace] = make(map[string]struct{})
		}
		c.pending[namespace][key] = struct{}{}
		unsent = append(unsent, item)
	}
	return unsent, nil
}

// MarkReported records events that the control plane accepted, so future
// resyncs do not emit them again.
func (c *EventCollector) MarkReported(namespace string, events []domain.KubernetesEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sent[namespace] == nil {
		c.sent[namespace] = make(map[string]struct{})
	}
	for _, event := range events {
		key := eventKey(event)
		delete(c.pending[namespace], key)
		c.sent[namespace][key] = struct{}{}
	}
}

// Release returns undelivered events to the next collection attempt.
func (c *EventCollector) Release(namespace string, events []domain.KubernetesEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range events {
		delete(c.pending[namespace], eventKey(event))
	}
}

func BuildEnvironmentEvents(events []KubernetesEvent, limit int) []domain.KubernetesEvent {
	if limit <= 0 {
		limit = 50
	}
	items := make([]domain.KubernetesEvent, 0, len(events))
	for _, event := range events {
		normalized := normalizeKubernetesEvent(event)
		if strings.TrimSpace(normalized.Message) == "" && strings.TrimSpace(normalized.Reason) == "" {
			continue
		}
		items = append(items, normalized)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return eventTimestamp(items[i]).After(eventTimestamp(items[j]))
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func normalizeKubernetesEvent(event KubernetesEvent) domain.KubernetesEvent {
	firstSeen := parseKubernetesTime(firstNonEmpty(event.FirstTimestamp, event.EventTime))
	lastSeen := parseKubernetesTime(firstNonEmpty(event.LastTimestamp, event.EventTime, event.FirstTimestamp))
	return domain.KubernetesEvent{
		UID:          firstNonEmpty(event.Metadata.UID, event.Metadata.Name),
		Namespace:    event.Metadata.Namespace,
		Type:         event.Type,
		Reason:       event.Reason,
		Message:      event.Message,
		InvolvedKind: event.InvolvedObject.Kind,
		InvolvedName: event.InvolvedObject.Name,
		Count:        event.Count,
		FirstSeen:    firstSeen,
		LastSeen:     lastSeen,
	}
}

func parseKubernetesTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC()
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}

func eventTimestamp(event domain.KubernetesEvent) time.Time {
	if !event.LastSeen.IsZero() {
		return event.LastSeen
	}
	return event.FirstSeen
}

func eventKey(event domain.KubernetesEvent) string {
	if uid := strings.TrimSpace(event.UID); uid != "" {
		return uid
	}
	return strings.Join([]string{event.Namespace, event.Type, event.Reason, event.Message, event.InvolvedKind, event.InvolvedName, eventTimestamp(event).UTC().Format(time.RFC3339Nano)}, "\x00")
}
