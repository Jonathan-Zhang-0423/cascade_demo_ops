package executor

import (
	"context"
	"errors"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type RecordingRenderPipelineRequest struct {
	SourcePackage      *model.ClientExecutionPackage
	CloudJobID         string
	RecordingOutputDir string
	RenderOutputDir    string
	ResultCreatedAt    time.Time
}

type RecordingRenderPipelineResult struct {
	RecordRequest          RecordRequest
	RecordResult           RecordResult
	RecordingResultPackage model.RecordingResultPackage
	RenderRequest          RenderRequest
	RenderResult           RenderResult
}

func RunClientExecutionRecordingAndRender(ctx context.Context, service Service, request RecordingRenderPipelineRequest) (RecordingRenderPipelineResult, error) {
	if service == nil {
		return RecordingRenderPipelineResult{}, errors.New("executor service is required")
	}
	if strings.TrimSpace(request.CloudJobID) == "" {
		return RecordingRenderPipelineResult{}, errors.New("cloud_job_id is required")
	}
	if strings.TrimSpace(request.RenderOutputDir) == "" {
		return RecordingRenderPipelineResult{}, errors.New("render_output_dir is required")
	}

	recordRequest, err := NewRecordRequestFromClientExecutionPackage(request.SourcePackage, request.RecordingOutputDir)
	if err != nil {
		return RecordingRenderPipelineResult{}, err
	}
	recordResult, err := service.Record(ctx, recordRequest)
	if err != nil {
		return RecordingRenderPipelineResult{}, err
	}
	recordingResultPackage, err := NewRecordingResultPackageFromRecordResult(request.SourcePackage, recordResult, request.CloudJobID, request.ResultCreatedAt)
	if err != nil {
		return RecordingRenderPipelineResult{}, err
	}

	result := RecordingRenderPipelineResult{
		RecordRequest:          recordRequest,
		RecordResult:           recordResult,
		RecordingResultPackage: recordingResultPackage,
	}
	renderRequest, err := NewRenderRequestFromRecordingResult(request.SourcePackage, &result.RecordingResultPackage, request.RenderOutputDir)
	if err != nil {
		return RecordingRenderPipelineResult{}, err
	}
	renderResult, err := service.Render(ctx, renderRequest)
	if err != nil {
		return RecordingRenderPipelineResult{}, err
	}
	result.RenderRequest = renderRequest
	result.RenderResult = renderResult
	return result, nil
}
