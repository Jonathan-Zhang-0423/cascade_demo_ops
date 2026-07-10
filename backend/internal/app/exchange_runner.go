package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

func (s *Service) RunUploadedExecutionPackage(ctx context.Context, orgID string, exchangePackageID string) (model.ExecutionPackageStatusResponse, error) {
	pkg, cloudJobID, err := s.exchange.StartExecution(ctx, orgID, exchangePackageID)
	if err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}

	workerPath := s.localVideoWorkerPath()
	if workerPath == "" {
		return s.failUploadedExecution(ctx, orgID, exchangePackageID, "video_worker_missing", errors.New("video worker path is not configured"))
	}
	if _, statErr := os.Stat(workerPath); statErr != nil {
		return s.failUploadedExecution(ctx, orgID, exchangePackageID, "video_worker_missing", statErr)
	}

	outputRoot := filepath.Join(s.runtime.ArtifactRoot, "exchange", safePathSegment(exchangePackageID))
	recordingDir := filepath.Join(outputRoot, "recording")
	renderDir := filepath.Join(outputRoot, "render")
	localDriver := driver.NewLocalDriver(s.runtime.NodeBinaryPath, workerPath)
	result, err := executor.RunClientExecutionRecordingAndRender(ctx, localDriver, executor.RecordingRenderPipelineRequest{
		SourcePackage:      &pkg,
		CloudJobID:         cloudJobID,
		RecordingOutputDir: recordingDir,
		RenderOutputDir:    renderDir,
		ResultCreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		return s.failUploadedExecution(ctx, orgID, exchangePackageID, "recording_render_failed", err)
	}
	return s.exchange.CompleteWithRecordingResult(ctx, orgID, exchangePackageID, result.RecordingResultPackage)
}

func (s *Service) failUploadedExecution(ctx context.Context, orgID string, exchangePackageID string, code string, err error) (model.ExecutionPackageStatusResponse, error) {
	status, failErr := s.exchange.FailExecution(ctx, orgID, exchangePackageID, code, err)
	if failErr != nil && status.ExchangePackageID == "" {
		return status, failErr
	}
	return status, nil
}

func (s *Service) localVideoWorkerPath() string {
	if s == nil {
		return ""
	}
	if s.runtime.SidecarPaths != nil && s.runtime.SidecarPaths["video-worker"] != "" {
		return s.runtime.SidecarPaths["video-worker"]
	}
	if s.runtime.DevRepoRoot != "" {
		return filepath.Join(s.runtime.DevRepoRoot, "video-worker", "dist", "index.js")
	}
	return ""
}

func safePathSegment(value string) string {
	if value == "" {
		return "item"
	}
	out := []rune(value)
	for index, char := range out {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			continue
		}
		out[index] = '_'
	}
	return string(out)
}
