package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/envplane/contracts/domain"
)

func (r *HTTPStatusReporter) FetchSecretMaterializationCommand(ctx context.Context, cfg Config) (*domain.AgentSecretMaterializationCommand, error) {
	query := url.Values{"projectId": {cfg.BootstrapProjectID}, "clusterId": {cfg.ClusterID}, "agentId": {cfg.AgentID}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/api/v1/agents/secret-materialization/commands/next?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.AgentAuthToken))
	response, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("fetch materialization command failed: status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var command domain.AgentSecretMaterializationCommand
	if err := json.NewDecoder(response.Body).Decode(&command); err != nil {
		return nil, err
	}
	if err := command.Validate(); err != nil {
		return nil, err
	}
	return &command, nil
}

func (r *HTTPStatusReporter) ReportSecretMaterializationResult(ctx context.Context, cfg Config, result domain.AgentSecretMaterializationResult) error {
	if err := result.Validate(); err != nil {
		return err
	}
	path := "/api/v1/agents/secret-materialization/commands/" + url.PathEscape(result.CommandID) + "/result"
	return r.postJSONWithBearer(ctx, path, result, cfg.AgentAuthToken, "report secret materialization result")
}

func RunSecretMaterializationCommands(ctx context.Context, cfg Config, reporter *HTTPStatusReporter, materializer *SecretMaterializer, logger *slog.Logger) {
	if reporter == nil || materializer == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := runSecretMaterializationCommandOnce(ctx, cfg, reporter, materializer, logger); err != nil && logger != nil {
			logger.Warn("secret materialization command failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runSecretMaterializationCommandOnce(ctx context.Context, cfg Config, reporter *HTTPStatusReporter, materializer *SecretMaterializer, logger *slog.Logger) error {
	command, err := reporter.FetchSecretMaterializationCommand(ctx, cfg)
	if err != nil || command == nil {
		return err
	}
	result := domain.AgentSecretMaterializationResult{ContractVersion: domain.SecretMaterializationCommandContractVersion, CommandID: command.CommandID, AttemptID: command.AttemptID, TenantID: command.TenantID, ProjectID: command.ProjectID, EnvironmentID: command.EnvironmentID, ClusterID: command.ClusterID, AgentID: command.AgentID, PlanID: command.PlanID, PlanDigest: command.PlanDigest, ExpectedRevision: command.ExpectedRevision, Status: domain.SecretCommandSucceeded, FinishedAt: time.Now().UTC()}
	runtimeCommand := MaterializationCommand{TenantID: command.TenantID, PlanID: command.PlanID, PlanDigest: command.PlanDigest, Audience: command.AgentID, Plan: command.Plan}
	if command.Operation == domain.SecretOperationCleanup {
		err = materializer.Cleanup(ctx, runtimeCommand)
	} else {
		var runtimeResults []MaterializationResult
		runtimeResults, err = materializer.Execute(ctx, runtimeCommand)
		result.Items, err = materializationWireResults(command.Plan, runtimeResults, result.FinishedAt)
	}
	if err != nil {
		result.Status = domain.SecretCommandFailed
		result.ErrorCode = materializationWireErrorCode(err)
	}
	if reportErr := reporter.ReportSecretMaterializationResult(ctx, cfg, result); reportErr != nil {
		return reportErr
	}
	if logger != nil {
		logger.Info("secret materialization command completed", "command_id", command.CommandID, "plan_id", command.PlanID, "status", result.Status, "error_code", result.ErrorCode)
	}
	return nil
}

func materializationWireResults(plan domain.SecretMaterializationPlan, results []MaterializationResult, finished time.Time) ([]domain.SecretMaterializationItemResult, error) {
	items := make(map[string]domain.SecretMaterializationItem, len(plan.Items))
	for _, item := range plan.Items {
		items[item.ID] = item
	}
	wire := make([]domain.SecretMaterializationItemResult, 0, len(results))
	for _, result := range results {
		item := items[result.ItemID]
		key, err := materializationIdempotencyKey(plan, result.ItemID, domain.SecretOperationMaterialize)
		if err != nil {
			return nil, err
		}
		state := domain.SecretItemReady
		if result.Status != "ready" {
			state = domain.SecretItemFailed
		}
		wire = append(wire, domain.SecretMaterializationItemResult{ItemID: result.ItemID, Strategy: item.Strategy, TargetNamespace: item.TargetNamespace, TargetName: item.TargetName, Operation: domain.SecretOperationMaterialize, IdempotencyKey: key, InputDigest: plan.InputDigest, Status: state, ErrorCode: materializationWireItemErrorCode(result.ErrorCode), Attempt: 1, StartedAt: finished, FinishedAt: finished})
	}
	return wire, nil
}

func materializationWireItemErrorCode(code string) domain.SecretMaterializationErrorCode {
	switch strings.TrimSpace(code) {
	case "":
		return ""
	case "foreign_secret", "conflict":
		return domain.SecretErrorConflict
	case "source_not_found":
		return domain.SecretErrorSourceNotFound
	case "permission_denied":
		return domain.SecretErrorPermissionDenied
	case "unsafe_secret_type", "invalid_binding", "validation_failed":
		return domain.SecretErrorValidationFailed
	case "timeout":
		return domain.SecretErrorTimeout
	default:
		return domain.SecretErrorBackendUnavailable
	}
}

func materializationWireErrorCode(err error) domain.SecretMaterializationErrorCode {
	switch {
	case errors.Is(err, ErrForeignSecret), errors.Is(err, ErrMaterializationConflict):
		return domain.SecretErrorConflict
	case errors.Is(err, ErrSecretNotFound):
		return domain.SecretErrorSourceNotFound
	case errors.Is(err, ErrMaterializationPlanMismatch), errors.Is(err, ErrUnsafeSecretType):
		return domain.SecretErrorValidationFailed
	default:
		return domain.SecretErrorBackendUnavailable
	}
}
