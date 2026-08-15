package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/envpilot/contracts/domain"
)

const environmentIDLabel = "envpilot.io/environment-id"

type NamespaceWatcher struct {
	source         NamespaceSource
	reporter       StatusReporter
	collector      *DeploymentStatusCollector
	eventCollector *EventCollector
	fluxCollector  *FluxStatusCollector
	resyncInterval time.Duration
	logger         *slog.Logger
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

func (w *NamespaceWatcher) Run(ctx context.Context) error {
	for {
		if err := w.SyncOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger.Error("namespace sync failed", "error", err)
		}
		if ctx.Err() != nil {
			return nil
		}
		err := w.source.WatchNamespaces(ctx, func(event NamespaceEvent) error {
			return w.reportEvent(ctx, event.Type, event.Namespace)
		})
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.logger.Error("namespace watch failed", "error", err)
		}

		timer := time.NewTimer(w.resyncInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (w *NamespaceWatcher) SyncOnce(ctx context.Context) error {
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
	return w.reportEventWithStatus(ctx, eventType, namespace, func(report NamespaceStatusReport) error {
		return w.reporter.ReportNamespaceStatus(ctx, report)
	}, func(environmentID string, events []domain.KubernetesEvent) error {
		return w.reporter.ReportEvents(ctx, environmentID, events)
	})
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
	if !strings.HasPrefix(name, "envpilot-pr-") {
		return ""
	}
	return strings.TrimPrefix(name, "envpilot-pr-")
}
