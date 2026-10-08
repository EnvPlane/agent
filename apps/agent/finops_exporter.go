package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	clusteragent "github.com/envplane/agent/agent"
)

func readPinnedPVCRefs(path string) ([]clusteragent.PinnedPVCUsageRef, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("approved PVC scope file unavailable")
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(data) > 16384 {
		return nil, errors.New("PVC scope file exceeds bounded size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var refs []clusteragent.PinnedPVCUsageRef
	if decoder.Decode(&refs) != nil || len(refs) < 1 || len(refs) > 5 {
		return nil, errors.New("invalid bounded PVC scope file")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing PVC scope data")
	}
	return refs, nil
}

// This standalone command never starts runtime registration, discovery,
// reconciliation or secret materialization. Its ServiceAccount needs only GET
// on the exact approved PVC names, and mounts must be operator-created read-only.
func runPVCUsageExporter(logger *slog.Logger) error {
	refs, err := readPinnedPVCRefs(os.Getenv("ENVPLANE_FINOPS_PVC_REFS_FILE"))
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(os.Getenv("ENVPLANE_FINOPS_EXPORTER_TLS_CERT_FILE"), os.Getenv("ENVPLANE_FINOPS_EXPORTER_TLS_KEY_FILE"))
	if err != nil {
		return errors.New("exporter TLS certificate unavailable")
	}
	cfg := clusteragent.ConfigFromEnv()
	// Scope comes solely from the reviewed pinned refs, not a broad watch setting.
	cfg.Namespaces = nil
	for _, ref := range refs {
		cfg.Namespaces = append(cfg.Namespaces, ref.Namespace)
	}
	source, err := clusteragent.NewKubernetesNamespaceSourceFromConfig(cfg)
	if err != nil {
		return errors.New("exporter Kubernetes client unavailable")
	}
	mountRoot := os.Getenv("ENVPLANE_FINOPS_PVC_MOUNT_ROOT")
	if mountRoot == "" {
		mountRoot = "/pvc-data"
	}
	sampl, err := clusteragent.NewPinnedPVCUsageSampler(mountRoot, refs, source.VerifyPinnedPVCUsageRef)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: ":9443", Handler: clusteragent.NewPinnedPVCUsageHandlerWithDiagnostic(sampl, func(reason string) {
			logger.Warn("PVC usage measurement unavailable", "reason", reason)
		}),
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServeTLS("", "") }()
	logger.Info("confined PVC usage HTTPS exporter started", "approved_claims", len(refs))
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("exporter HTTPS listener failed")
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
	return nil
}
