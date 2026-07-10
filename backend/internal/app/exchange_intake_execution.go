package app

import (
	"context"
	"errors"

	"cascade-demoops/backend/internal/model"
)

func (s *ExchangeIntakeService) StartExecution(ctx context.Context, orgID string, exchangePackageID string) (model.ClientExecutionPackage, string, error) {
	if err := ctx.Err(); err != nil {
		return model.ClientExecutionPackage{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.packageStateLocked(orgID, exchangePackageID)
	if err != nil {
		return model.ClientExecutionPackage{}, "", err
	}
	if state.Status == model.ExchangePackageStatusCompleted {
		return model.ClientExecutionPackage{}, "", errors.New("exchange package is already completed")
	}
	if state.Status == model.ExchangePackageStatusFailed || state.Status == model.ExchangePackageStatusCanceled || state.Status == model.ExchangePackageStatusExpired {
		return model.ClientExecutionPackage{}, "", errors.New("exchange package is not runnable")
	}
	if state.Payload.PackageID == "" {
		return model.ClientExecutionPackage{}, "", errors.New("exchange package payload is not available")
	}
	setPackageStageLocked(state, model.ExchangePackageStatusRunning, "validated", "Execution package passed cloud-side validation and is ready to prepare the worker.", 25, s.now())
	return state.Payload, state.CloudJobID, nil
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
	setPackageStageLocked(state, model.ExchangePackageStatusRunning, stage, message, progress, s.now())
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
	state.FailedStage = failedStage
	setPackageStageLocked(state, model.ExchangePackageStatusFailed, "failed", message, 100, s.now())
	state.Error = &model.AgentError{Code: code, Message: message}
	if failedStage != "failed" {
		state.Error.EvidenceRefs = append(state.Error.EvidenceRefs, model.EvidenceRef{ID: "failed_stage_" + failedStage, Kind: model.EvidenceKindExecutionRun, Summary: failedStage})
	}
	return s.statusResponseLocked(state), err
}
