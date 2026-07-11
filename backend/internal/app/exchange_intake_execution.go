package app

import (
	"context"
	"errors"
	"time"

	"cascade-demoops/backend/internal/model"
)

type executionStartResult struct {
	Payload    model.ClientExecutionPackage
	CloudJobID string
	Status     model.ExecutionPackageStatusResponse
	Started    bool
	TimeoutSec int
}

func (s *ExchangeIntakeService) StartExecution(ctx context.Context, orgID string, exchangePackageID string) (model.ClientExecutionPackage, string, error) {
	result, err := s.TryStartExecution(ctx, orgID, exchangePackageID)
	if err != nil {
		return model.ClientExecutionPackage{}, "", err
	}
	if !result.Started {
		return model.ClientExecutionPackage{}, result.CloudJobID, errors.New("exchange package execution is already started or finished")
	}
	return result.Payload, result.CloudJobID, nil
}

func (s *ExchangeIntakeService) TryStartExecution(ctx context.Context, orgID string, exchangePackageID string) (executionStartResult, error) {
	if err := ctx.Err(); err != nil {
		return executionStartResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.packageStateLocked(orgID, exchangePackageID)
	if err != nil {
		return executionStartResult{}, err
	}
	status := s.statusResponseLocked(state)
	if state.Status == model.ExchangePackageStatusCompleted {
		return executionStartResult{CloudJobID: state.CloudJobID, Status: status, TimeoutSec: state.Envelope.Policy.MaxExecutionWindowSec}, nil
	}
	if state.Status == model.ExchangePackageStatusFailed || state.Status == model.ExchangePackageStatusCanceled || state.Status == model.ExchangePackageStatusExpired {
		return executionStartResult{CloudJobID: state.CloudJobID, Status: status, TimeoutSec: state.Envelope.Policy.MaxExecutionWindowSec}, nil
	}
	if state.Status == model.ExchangePackageStatusRunning || state.Status == model.ExchangePackageStatusQueued {
		return executionStartResult{CloudJobID: state.CloudJobID, Status: status, TimeoutSec: state.Envelope.Policy.MaxExecutionWindowSec}, nil
	}
	if state.Payload.PackageID == "" {
		return executionStartResult{}, errors.New("exchange package payload is not available")
	}
	setPackageStageLocked(state, model.ExchangePackageStatusRunning, "validated", "Execution package passed cloud-side validation and is ready to prepare the worker.", 25, s.now())
	if err := s.saveLocked(ctx); err != nil {
		return executionStartResult{}, err
	}
	return executionStartResult{
		Payload:    state.Payload,
		CloudJobID: state.CloudJobID,
		Status:     s.statusResponseLocked(state),
		Started:    true,
		TimeoutSec: state.Envelope.Policy.MaxExecutionWindowSec,
	}, nil
}

func (s *ExchangeIntakeService) MarkExecutionStage(ctx context.Context, orgID string, exchangePackageID string, stage string, message string, progress int) (model.ExecutionPackageStatusResponse, error) {
	if errCtx := ctx.Err(); errCtx != nil {
		return model.ExecutionPackageStatusResponse{}, errCtx
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.packageStateLocked(orgID, exchangePackageID)
	if err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	if isTerminalExchangeStatus(state.Status) {
		return s.statusResponseLocked(state), nil
	}
	setPackageStageLocked(state, model.ExchangePackageStatusRunning, stage, message, progress, s.now())
	if err := s.saveLocked(ctx); err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	return s.statusResponseLocked(state), nil
}

func (s *ExchangeIntakeService) CancelExecution(ctx context.Context, orgID string, exchangePackageID string, reason string) (model.ExecutionPackageStatusResponse, error) {
	if errCtx := ctx.Err(); errCtx != nil {
		return model.ExecutionPackageStatusResponse{}, errCtx
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.packageStateLocked(orgID, exchangePackageID)
	if err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	if isTerminalExchangeStatus(state.Status) {
		return s.statusResponseLocked(state), nil
	}
	if reason == "" {
		reason = "canceled"
	}
	setPackageStageLocked(state, model.ExchangePackageStatusCanceled, "canceled", "Execution was canceled before completion.", 100, s.now())
	state.Error = &model.AgentError{Code: reason, Message: "Execution was canceled before completion."}
	if err := s.saveLocked(ctx); err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	return s.statusResponseLocked(state), nil
}

func (s *ExchangeIntakeService) FailExecution(ctx context.Context, orgID string, exchangePackageID string, code string, err error) (model.ExecutionPackageStatusResponse, error) {
	if errCtx := ctx.Err(); errCtx != nil {
		return model.ExecutionPackageStatusResponse{}, errCtx
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, stateErr := s.packageStateLocked(orgID, exchangePackageID)
	if stateErr != nil {
		return model.ExecutionPackageStatusResponse{}, stateErr
	}
	if isTerminalExchangeStatus(state.Status) {
		return s.statusResponseLocked(state), nil
	}
	message := "execution failed"
	if err != nil {
		message = err.Error()
	}
	if code == "" {
		code = "execution_failed"
	}
	failedStage := state.Stage
	if failedStage == "" {
		failedStage = "failed"
	}
	markPackageFailedLocked(state, code, message, failedStage, s.now())
	if saveErr := s.saveLocked(ctx); saveErr != nil {
		return model.ExecutionPackageStatusResponse{}, saveErr
	}
	return s.statusResponseLocked(state), err
}

func markPackageFailedLocked(state *exchangePackageState, code string, message string, failedStage string, failedAt time.Time) {
	if state == nil {
		return
	}
	if code == "" {
		code = "execution_failed"
	}
	if message == "" {
		message = "execution failed"
	}
	if failedStage == "" {
		failedStage = state.Stage
	}
	if failedStage == "" {
		failedStage = "failed"
	}
	state.FailedStage = failedStage
	setPackageStageLocked(state, model.ExchangePackageStatusFailed, "failed", message, 100, failedAt)
	state.Error = &model.AgentError{Code: code, Message: message}
	if failedStage != "failed" {
		state.Error.EvidenceRefs = append(state.Error.EvidenceRefs, model.EvidenceRef{ID: "failed_stage_" + failedStage, Kind: model.EvidenceKindExecutionRun, Summary: failedStage})
	}
}

func isTerminalExchangeStatus(status model.ExchangePackageStatus) bool {
	return status == model.ExchangePackageStatusCompleted ||
		status == model.ExchangePackageStatusFailed ||
		status == model.ExchangePackageStatusCanceled ||
		status == model.ExchangePackageStatusExpired
}
