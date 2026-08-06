package executor

import (
	"errors"

	"cascade-demoops/backend/internal/model"
)

func NewRenderRequestFromRecordingResult(source *model.ClientExecutionPackage, result *model.RecordingResultPackage, outputDir string) (RenderRequest, error) {
	if source == nil {
		return RenderRequest{}, errors.New("client execution package is required")
	}
	var validationErr error
	if result != nil && result.Delivery.RecipientKind == "local_test_only" {
		validationErr = model.ValidateLocalTestRecordingResultPackageForRender(result, source)
	} else {
		validationErr = model.ValidateRecordingResultPackageForRender(result, source)
	}
	if validationErr != nil {
		return RenderRequest{}, validationErr
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
