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
	state.Status = model.ExchangePackageStatusRunning
	state.Stage = "recording_rendering"
	state.Message = "Recording and rendering are running."
	state.ProgressPercent = 50
	state.UpdatedAt = s.now()
	return state.Payload, state.CloudJobID, nil
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
	state.Status = model.ExchangePackageStatusFailed
	state.Stage = "failed"
	state.Message = message
	state.ProgressPercent = 100
	state.Error = &model.AgentError{Code: code, Message: message}
	state.UpdatedAt = s.now()
	return s.statusResponseLocked(state), err
}
