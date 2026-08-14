package model

import (
	"strings"
	"testing"
)

func TestFormalCompletedResultRequiresRequestedAuditArtifacts(t *testing.T) {
	source := &ClientExecutionPackage{RecordingRunSpec: RecordingRunSpec{Outputs: RecordingOutputRequest{FinalVideo: true, Trace: true, ScreenshotPack: true}}}
	hash := strings.Repeat("a", 64)
	result := &RecordingResultPackage{
		Status:            RecordingResultStatusGenerated,
		ExecutionTrace:    &ExecutionTrace{},
		StepResults:       []StepResult{{NodeID: "node_1", Status: "passed"}},
		ValidationReports: []ValidationReport{{ReportID: "report_1", Decision: ValidationDecisionContinue}},
		StageEventLogRef:  &ArtifactRef{ID: "events", Kind: "browser_agent_stage_event_log", URI: "direct://events", SHA256: hash, SizeBytes: 10},
	}
	if err := ValidateFormalRecordingResultArtifacts(result, source); err == nil || !strings.Contains(err.Error(), "result_missing_final_mp4") {
		t.Fatalf("missing MP4 must block formal completion, got %v", err)
	}
	result.GeneratedAssets = []ArtifactRef{
		{ID: "video", Kind: "demo_video", URI: "direct://video", MimeType: "video/mp4", SHA256: hash, SizeBytes: 10},
		{ID: "trace", Kind: "browser_trace", URI: "direct://trace", SHA256: hash, SizeBytes: 10},
		{ID: "shot", Kind: "screenshot", URI: "direct://shot", SHA256: hash, SizeBytes: 10},
		{ID: "catalog", Kind: "asset_timeline_catalog", URI: "direct://catalog", SHA256: hash, SizeBytes: 10},
		{ID: "edit_plan", Kind: "demo_edit_plan", URI: "direct://edit-plan", SHA256: hash, SizeBytes: 10},
	}
	result.StageEventLogRef = &ArtifactRef{ID: "events", Kind: "browser_agent_stage_event_log", URI: "direct://events", SHA256: hash, SizeBytes: 10}
	if err := ValidateFormalRecordingResultArtifacts(result, source); err != nil {
		t.Fatalf("complete formal result was rejected: %v", err)
	}
}

func TestFormalOutlineResultRequiresReplayAndEditorHandoffArtifacts(t *testing.T) {
	tests := []struct {
		name   string
		code   string
		mutate func(*RecordingResultPackage)
	}{
		{name: "step results", code: "result_missing_step_results", mutate: func(result *RecordingResultPackage) { result.StepResults = nil }},
		{name: "validation reports", code: "result_missing_validation_reports", mutate: func(result *RecordingResultPackage) { result.ValidationReports = nil }},
		{name: "raw recording", code: "result_missing_raw_recording", mutate: func(result *RecordingResultPackage) {
			result.GeneratedAssets = removeFormalArtifact(result.GeneratedAssets, "raw_recording")
		}},
		{name: "replay manifest", code: "result_missing_replay_manifest", mutate: func(result *RecordingResultPackage) {
			result.GeneratedAssets = removeFormalArtifact(result.GeneratedAssets, "replay_manifest")
		}},
		{name: "timeline catalog", code: "result_missing_asset_timeline_catalog", mutate: func(result *RecordingResultPackage) {
			result.GeneratedAssets = removeFormalArtifact(result.GeneratedAssets, "asset_timeline_catalog")
		}},
		{name: "demo edit plan", code: "result_missing_demo_edit_plan", mutate: func(result *RecordingResultPackage) {
			result.GeneratedAssets = removeFormalArtifact(result.GeneratedAssets, "demo_edit_plan")
		}},
		{name: "raw checksum", code: "result_missing_raw_recording", mutate: func(result *RecordingResultPackage) {
			for index := range result.GeneratedAssets {
				if result.GeneratedAssets[index].Kind == "raw_recording" {
					result.GeneratedAssets[index].SHA256 = ""
				}
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, result := completeFormalOutlineResult()
			test.mutate(result)
			if err := ValidateFormalRecordingResultArtifacts(result, source); err == nil || !strings.Contains(err.Error(), test.code) {
				t.Fatalf("expected %s, got %v", test.code, err)
			}
		})
	}
	source, result := completeFormalOutlineResult()
	if err := ValidateFormalRecordingResultArtifacts(result, source); err != nil {
		t.Fatalf("complete formal outline result was rejected: %v", err)
	}
}

func completeFormalOutlineResult() (*ClientExecutionPackage, *RecordingResultPackage) {
	hash := strings.Repeat("b", 64)
	source := &ClientExecutionPackage{
		ExecutableScriptBundle: &ExecutableRecordingScriptBundle{ScriptManifest: ExecutableScriptManifest{Runtime: ExecutableScriptRuntimeBrowserAgentOutlineV1}},
		RecordingRunSpec:       RecordingRunSpec{Outputs: RecordingOutputRequest{RawRecording: true, FinalVideo: true, ScreenshotPack: true, Trace: true}},
	}
	artifact := func(id, kind, mime string) ArtifactRef {
		return ArtifactRef{ID: id, Kind: kind, URI: "direct://artifacts/" + id, MimeType: mime, SHA256: hash, SizeBytes: 10}
	}
	result := &RecordingResultPackage{
		Status: RecordingResultStatusGenerated, ExecutionTrace: &ExecutionTrace{},
		StepResults:       []StepResult{{NodeID: "node_1", Status: "passed"}},
		ValidationReports: []ValidationReport{{ReportID: "report_1", Decision: ValidationDecisionContinue}},
		GeneratedAssets: []ArtifactRef{
			artifact("raw", "raw_recording", "video/webm"),
			artifact("video", "demo_video", "video/mp4"),
			artifact("trace", "browser_trace", "application/zip"),
			artifact("shot", "screenshot", "image/png"),
			artifact("replay", "replay_manifest", "application/json"),
			artifact("catalog", "asset_timeline_catalog", "application/json"),
			artifact("edit-plan", "demo_edit_plan", "application/json"),
		},
		StageEventLogRef: &ArtifactRef{ID: "events", Kind: "browser_agent_stage_event_log", URI: "direct://artifacts/events", SHA256: hash, SizeBytes: 10},
	}
	return source, result
}

func removeFormalArtifact(values []ArtifactRef, kind string) []ArtifactRef {
	out := make([]ArtifactRef, 0, len(values))
	for _, value := range values {
		if value.Kind != kind {
			out = append(out, value)
		}
	}
	return out
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
