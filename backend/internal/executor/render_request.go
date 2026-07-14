package executor

import (
	"errors"

	"cascade-demoops/backend/internal/model"
)

func NewRenderRequestFromRecordingResult(source *model.ClientExecutionPackage, result *model.RecordingResultPackage, outputDir string) (RenderRequest, error) {
	if source == nil {
		return RenderRequest{}, errors.New("client execution package is required")
	}
	if err := model.ValidateRecordingResultPackageForRender(result, source); err != nil {
		return RenderRequest{}, err
	}
	durationSec := source.RecordingRunSpec.Timeline.TargetDurationSec
	request := RenderRequest{
		Graph:                  source.WorkflowGraph,
		OutputDir:              outputDir,
		DurationSec:            durationSec,
		RecordingRunSpec:       &source.RecordingRunSpec,
		ExecutionTrace:         result.ExecutionTrace,
		GeneratedAssets:        result.GeneratedAssets,
		RecordingResultPackage: result,
	}
	if len(request.GeneratedAssets) == 0 && result.ExecutionTrace != nil {
		request.GeneratedAssets = result.ExecutionTrace.Artifacts
	}
	return request, nil
}
