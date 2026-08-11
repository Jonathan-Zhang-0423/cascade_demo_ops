package model

import (
	"strings"
	"testing"
)

func TestFormalCompletedResultRequiresRequestedAuditArtifacts(t *testing.T) {
	source := &ClientExecutionPackage{RecordingRunSpec: RecordingRunSpec{Outputs: RecordingOutputRequest{FinalVideo: true, Trace: true, ScreenshotPack: true}}}
	result := &RecordingResultPackage{Status: RecordingResultStatusGenerated, ExecutionTrace: &ExecutionTrace{}}
	if err := ValidateFormalRecordingResultArtifacts(result, source); err == nil || !strings.Contains(err.Error(), "result_missing_final_mp4") {
		t.Fatalf("missing MP4 must block formal completion, got %v", err)
	}
	result.GeneratedAssets = []ArtifactRef{
		{ID: "video", Kind: "demo_video", URI: "direct://video", MimeType: "video/mp4"},
		{ID: "trace", Kind: "browser_trace", URI: "direct://trace"},
		{ID: "shot", Kind: "screenshot", URI: "direct://shot"},
	}
	result.StageEventLogRef = &ArtifactRef{ID: "events", Kind: "browser_agent_stage_event_log", URI: "direct://events"}
	if err := ValidateFormalRecordingResultArtifacts(result, source); err != nil {
		t.Fatalf("complete formal result was rejected: %v", err)
	}
}

func TestFormalFailedResultRequiresTraceableDiagnostic(t *testing.T) {
	source := &ClientExecutionPackage{}
	result := &RecordingResultPackage{Status: RecordingResultStatusFailed}
	if err := ValidateFormalRecordingResultArtifacts(result, source); err == nil || !strings.Contains(err.Error(), "failed_result_missing_diagnostic") {
		t.Fatalf("missing diagnostic must block formal failure result, got %v", err)
	}
	result.FailureDiagnostic = &ScriptFailureDiagnostic{ScreenshotRefs: []PackageArtifactDescriptor{{ID: "shot", Kind: "screenshot", URI: "direct://shot"}}}
	if err := ValidateFormalRecordingResultArtifacts(result, source); err != nil {
		t.Fatalf("traceable failed result was rejected: %v", err)
	}
}
