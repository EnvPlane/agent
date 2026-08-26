package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/envplane/contracts/domain"
)

const environmentIDLabel = "envplane.io/environment-id"

const (
	watchReportAttempts       = 5
	watchReportInitialBackoff = 250 * time.Millisecond
)

type NamespaceWatcher struct {
	source           NamespaceSource
	reporter         StatusReporter
	collector        *DeploymentStatusCollector
	eventCollector   *EventCollector
	fluxCollector    *FluxStatusCollector
	resyncInterval   time.Duration
	logger           *slog.Logger
	terminalQueueDir string
}

type batchStatusReporter interface {
	ReportNamespaceStatusBatch(context.Context, []NamespaceStatusReport) error
}

type batchEventReporter interface {
	ReportEventsBatch(context.Context, []EnvironmentEventsReport) error
}

type EnvironmentEventsReport struct {
	EnvironmentID string
	Events        []domain.KubernetesEvent
}

func NewNamespaceWatcher(source NamespaceSource, reporter StatusReporter, resyncInterval time.Duration, logger *slog.Logger) *NamespaceWatcher {
	var collector *DeploymentStatusCollector
	if workloadSource, ok := source.(WorkloadSource); ok {
		collector = NewDeploymentStatusCollector(workloadSource)
	}
	var eventCollector *EventCollector
	if eventSource, ok := source.(EventSource); ok {
		eventCollector = NewEventCollector(eventSource)
	}
	var fluxCollector *FluxStatusCollector
	if fluxSource, ok := source.(FluxSource); ok {
		fluxCollector = NewFluxStatusCollector(fluxSource)
	}
	return NewNamespaceWatcherWithCollectors(source, reporter, collector, eventCollector, fluxCollector, resyncInterval, logger)
}

func NewNamespaceWatcherWithCollector(source NamespaceSource, reporter StatusReporter, collector *DeploymentStatusCollector, resyncInterval time.Duration, logger *slog.Logger) *NamespaceWatcher {
	return NewNamespaceWatcherWithCollectors(source, reporter, collector, nil, nil, resyncInterval, logger)
}

func NewNamespaceWatcherWithCollectors(source NamespaceSource, reporter StatusReporter, collector *DeploymentStatusCollector, eventCollector *EventCollector, fluxCollector *FluxStatusCollector, resyncInterval time.Duration, logger *slog.Logger) *NamespaceWatcher {
	if resyncInterval <= 0 {
		resyncInterval = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &NamespaceWatcher{
		source:         source,
		reporter:       reporter,
		collector:      collector,
		eventCollector: eventCollector,
		fluxCollector:  fluxCollector,
		resyncInterval: resyncInterval,
		logger:         logger,
	}
}

// SetTerminalEventQueueDir enables durable storage for namespace deletion
// events that remain undelivered after bounded retries.
func (w *NamespaceWatcher) SetTerminalEventQueueDir(dir string) {
	w.terminalQueueDir = strings.TrimSpace(dir)
}

func (w *NamespaceWatcher) Run(ctx context.Context) error {
	for {
		w.drainTerminalEventQueue(ctx)
		if err := w.SyncOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger.Error("namespace sync failed", "error", err)
		}
		if ctx.Err() != nil {
			return nil
		}

		watchDone := make(chan error, 1)
		go func() {
			watchDone <- w.source.WatchNamespaces(ctx, func(event NamespaceEvent) error {
				return w.reportEvent(ctx, event.Type, event.Namespace)
			})
		}()

		ticker := time.NewTicker(w.resyncInterval)
		watchActive := true
		for watchActive {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return nil
			case <-ticker.C:
				if err := w.SyncOnce(ctx); err != nil && ctx.Err() == nil {
					w.logger.Error("namespace sync failed", "error", err)
				}
			case err := <-watchDone:
				watchActive = false
				if ctx.Err() != nil {
					ticker.Stop()
					return nil
				}
				if err != nil {
					w.logger.Error("namespace watch failed", "error", err)
				}
			}
		}
		ticker.Stop()
	}
}

func (w *NamespaceWatcher) SyncOnce(ctx context.Context) error {
	w.drainTerminalEventQueue(ctx)
	namespaces, err := w.source.ListNamespaces(ctx)
	if err != nil {
		return err
	}
	workers := len(namespaces)
	if workers > 8 {
		workers = 8
	}
	if workers == 0 {
		return nil
	}
	queue := make(chan Namespace)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	var batchMu sync.Mutex
	var batchReports []NamespaceStatusReport
	var batchEvents []EnvironmentEventsReport
	batch, useBatch := w.reporter.(batchStatusReporter)
	eventBatch, useEventBatch := w.reporter.(batchEventReporter)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for namespace := range queue {
				statusSink := func(report NamespaceStatusReport) error {
					if !useBatch {
						return w.reporter.ReportNamespaceStatus(ctx, report)
					}
					batchMu.Lock()
					batchReports = append(batchReports, report)
					batchMu.Unlock()
					return nil
				}
				eventSink := func(environmentID string, events []domain.KubernetesEvent) error {
					if !useEventBatch {
						return w.reporter.ReportEvents(ctx, environmentID, events)
					}
					batchMu.Lock()
					batchEvents = append(batchEvents, EnvironmentEventsReport{EnvironmentID: environmentID, Events: events})
					batchMu.Unlock()
					return nil
				}
				if err := w.reportEventWithStatus(ctx, "SYNC", namespace, statusSink, eventSink); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					w.logger.Error("namespace status report failed", "namespace", namespace.Metadata.Name, "error", err)
				}
			}
		}()
	}
	for _, namespace := range namespaces {
		select {
		case queue <- namespace:
		case <-ctx.Done():
			close(queue)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(queue)
	wg.Wait()
	if useBatch && len(batchReports) > 0 {
		if err := batch.ReportNamespaceStatusBatch(ctx, batchReports); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if useEventBatch && len(batchEvents) > 0 {
		if err := eventBatch.ReportEventsBatch(ctx, batchEvents); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	var syncErr = firstErr
	return syncErr
}

func (w *NamespaceWatcher) reportEvent(ctx context.Context, eventType string, namespace Namespace) error {
	var err error
	backoff := watchReportInitialBackoff
	for attempt := 1; attempt <= watchReportAttempts; attempt++ {
		err = w.reportEventWithStatus(ctx, eventType, namespace, func(report NamespaceStatusReport) error {
			return w.reporter.ReportNamespaceStatus(ctx, report)
		}, func(environmentID string, events []domain.KubernetesEvent) error {
			return w.reporter.ReportEvents(ctx, environmentID, events)
		})
		if err == nil || ctx.Err() != nil || attempt == watchReportAttempts {
			if err != nil && strings.EqualFold(eventType, "DELETED") {
				if queueErr := w.enqueueTerminalEvent(eventType, namespace); queueErr != nil {
					w.logger.Error("persist terminal namespace event failed", "namespace", namespace.Metadata.Name, "error", queueErr)
				}
				return nil
			}
			return err
		}
		w.logger.Warn("namespace event report failed; retrying", "namespace", namespace.Metadata.Name, "event", eventType, "attempt", attempt, "error", err)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff *= 2
	}
	return err
}

type queuedNamespaceEvent struct {
	Type      string    `json:"type"`
	Namespace Namespace `json:"namespace"`
}

func (w *NamespaceWatcher) enqueueTerminalEvent(eventType string, namespace Namespace) error {
	if w.terminalQueueDir == "" {
		return fmt.Errorf("terminal event queue is not configured")
	}
	if err := os.MkdirAll(w.terminalQueueDir, 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(queuedNamespaceEvent{Type: eventType, Namespace: namespace})
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%s.json", time.Now().UnixNano(), strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(namespace.Metadata.Name))
	tmp, err := os.CreateTemp(w.terminalQueueDir, ".event-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(w.terminalQueueDir, name))
}

func (w *NamespaceWatcher) drainTerminalEventQueue(ctx context.Context) {
	if w.terminalQueueDir == "" {
		return
	}
	entries, err := os.ReadDir(w.terminalQueueDir)
	if err != nil {
		return
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			paths = append(paths, filepath.Join(w.terminalQueueDir, entry.Name()))
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var queued queuedNamespaceEvent
		if json.Unmarshal(data, &queued) != nil {
			continue
		}
		if err := w.reportEventWithStatus(ctx, queued.Type, queued.Namespace, func(report NamespaceStatusReport) error { return w.reporter.ReportNamespaceStatus(ctx, report) }, func(environmentID string, events []domain.KubernetesEvent) error {
			return w.reporter.ReportEvents(ctx, environmentID, events)
		}); err != nil {
			continue
		}
		_ = os.Remove(path)
	}
}

func (w *NamespaceWatcher) reportEventWithStatus(ctx context.Context, eventType string, namespace Namespace, reportStatus func(NamespaceStatusReport) error, reportEvents func(string, []domain.KubernetesEvent) error) error {
	report, ok := BuildNamespaceStatusReport(eventType, namespace)
	if !ok {
		w.logger.Debug("namespace skipped", "namespace", namespace.Metadata.Name, "event", eventType)
		return nil
	}
	if w.collector != nil && report.Status != domain.StatusTerminating && report.Status != domain.StatusTerminated {
		workloadReport, err := w.collector.Collect(ctx, namespace.Metadata.Name)
		if err != nil {
			report.Status = domain.StatusFailed
			report.Message = namespaceStatusMessage(eventType, namespace, report.Status) + "; deployment collector failed: " + err.Error()
		} else {
			report.Status = workloadReport.Status
			report.Message = namespaceStatusMessage(eventType, namespace, report.Status) + "; " + workloadReport.Message
		}
	}
	if err := reportStatus(report); err != nil {
		return err
	}
	if w.eventCollector != nil && report.Status != domain.StatusTerminated {
		events, err := w.eventCollector.Collect(ctx, namespace.Metadata.Name)
		if err != nil {
			w.logger.Error("kubernetes events collection failed", "environment", report.EnvironmentID, "namespace", namespace.Metadata.Name, "error", err)
		} else if err := reportEvents(report.EnvironmentID, events); err != nil {
			w.logger.Error("kubernetes events report failed", "environment", report.EnvironmentID, "namespace", namespace.Metadata.Name, "error", err)
		} else {
			w.logger.Info("kubernetes events reported", "environment", report.EnvironmentID, "namespace", namespace.Metadata.Name, "count", len(events))
		}
	}
	if w.fluxCollector != nil && report.Status != domain.StatusTerminated {
		fluxStatus, err := w.fluxCollector.Collect(ctx, report.EnvironmentID, namespace)
		if err != nil {
			w.logger.Error("flux status collection failed", "environment", report.EnvironmentID, "namespace", namespace.Metadata.Name, "error", err)
		} else if err := w.reporter.ReportFluxStatus(ctx, report.EnvironmentID, fluxStatus); err != nil {
			w.logger.Error("flux status report failed", "environment", report.EnvironmentID, "namespace", namespace.Metadata.Name, "error", err)
		} else {
			w.logger.Info("flux status reported", "environment", report.EnvironmentID, "namespace", namespace.Metadata.Name, "status", fluxStatus.Status)
		}
	}
	w.logger.Info("namespace status reported", "environment", report.EnvironmentID, "namespace", report.Namespace, "status", report.Status, "event", eventType)
	return nil
}

func BuildNamespaceStatusReport(eventType string, namespace Namespace) (NamespaceStatusReport, bool) {
	environmentID := strings.TrimSpace(namespace.Metadata.Labels[environmentIDLabel])
	if environmentID == "" {
		environmentID = environmentIDFromNamespace(namespace.Metadata.Name)
	}
	if environmentID == "" {
		return NamespaceStatusReport{}, false
	}

	status := namespaceStatus(eventType, namespace)
	return NamespaceStatusReport{
		EnvironmentID: environmentID,
		Namespace:     namespace.Metadata.Name,
		Status:        status,
		Message:       namespaceStatusMessage(eventType, namespace, status),
		EventType:     eventType,
		Phase:         namespace.Status.Phase,
	}, true
}

func namespaceStatus(eventType string, namespace Namespace) domain.EnvironmentStatus {
	if strings.EqualFold(eventType, "DELETED") {
		return domain.StatusTerminated
	}
	if namespace.Metadata.DeletionTimestamp != "" || strings.EqualFold(namespace.Status.Phase, "Terminating") {
		return domain.StatusTerminating
	}
	if namespace.Status.Phase == "" || strings.EqualFold(namespace.Status.Phase, "Active") {
		return domain.StatusReady
	}
	return domain.StatusFailed
}

func namespaceStatusMessage(eventType string, namespace Namespace, status domain.EnvironmentStatus) string {
	phase := strings.TrimSpace(namespace.Status.Phase)
	if phase == "" {
		phase = "unknown"
	}
	return fmt.Sprintf("namespace %s event=%s phase=%s status=%s", namespace.Metadata.Name, eventType, phase, status)
}

func environmentIDFromNamespace(name string) string {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, "envplane-pr-") {
		return ""
	}
	return strings.TrimPrefix(name, "envplane-pr-")
}
