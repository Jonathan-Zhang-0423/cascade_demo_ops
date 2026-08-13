package executor

import (
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestNewRenderRequestFromRecordingResultUsesTeamProtocolPayloads(t *testing.T) {
	graph := model.NewDemoWorkflowGraph("graph_1", "project_1", "https://app.example.com")
	source := &model.ClientExecutionPackage{
		PackageID:     "pkg_1",
		OrgID:         "org_1",
		ProjectID:     "project_1",
		SchemaVersion: model.ClientExecutionPackageSchemaVersion,
		WorkflowGraph: graph,
		RecordingRunSpec: model.RecordingRunSpec{
			Timeline: model.RecordingTimeline{TargetDurationSec: 45},
		},
	}
	result := &model.RecordingResultPackage{
		ResultID:        "result_1",
		SourcePackageID: source.PackageID,
		SchemaVersion:   model.RecordingResultPackageSchemaVersion,
		Status:          model.RecordingResultStatusGenerated,
		ExecutionTrace: &model.ExecutionTrace{
			ID:              "trace_1",
			WorkflowGraphID: graph.ID,
			GraphVersion:    graph.Version,
			StepResults:     []model.StepResult{{NodeID: "start", Status: "passed"}},
			Artifacts:       []model.ArtifactRef{{ID: "artifact_raw_recording", Kind: "raw_recording", URI: "file:///tmp/recording.webm"}},
		},
		GeneratedAssets: []model.ArtifactRef{{ID: "artifact_raw_recording", Kind: "raw_recording", URI: "file:///tmp/recording.webm"}},
		Delivery: model.ResultDelivery{
			ResultPackageRef: model.PackageArtifactDescriptor{
				ID:        "result_package_artifact",
				Role:      "recording_result",
				Kind:      "recording_result_package",
				URI:       "s3://cascade-results/result.json.enc",
				SHA256:    "sha_result",
				Encrypted: true,
				Sensitive: true,
			},
			RecipientKind:  model.ResultRecipientAppInstallation,
			RecipientKeyID: "install_result_key_1",
			EncryptionAlg:  model.CryptoSuiteXChaCha20Poly1305,
			AckRequired:    true,
		},
	}

	request, err := NewRenderRequestFromRecordingResult(source, result, "artifacts/render")
	if err != nil {
		t.Fatal(err)
	}
	if request.Graph != graph || request.RecordingResultPackage != result || request.ExecutionTrace != result.ExecutionTrace {
		t.Fatalf("render request did not preserve protocol payload refs: %+v", request)
	}
	if request.DurationSec != 45 || request.OutputDir != "artifacts/render" {
		t.Fatalf("render request lost run spec settings: %+v", request)
	}
	if request.RenderProfile == nil || request.RenderProfile.Mode != "final" || request.RenderProfile.Width != 2560 || request.RenderProfile.Height != 1440 || request.RenderProfile.FPS != 30 || request.RenderProfile.CRF != 18 || request.RenderProfile.Format != "mp4" {
		t.Fatalf("formal Browser Agent render request lost the canonical 2K delivery profile: %+v", request.RenderProfile)
	}
	if len(request.GeneratedAssets) != 1 || request.GeneratedAssets[0].ID != "artifact_raw_recording" {
		t.Fatalf("render request lost generated assets: %+v", request.GeneratedAssets)
	}
}

func TestNewRenderRequestFromRecordingResultRejectsMismatchedSource(t *testing.T) {
	graph := model.NewDemoWorkflowGraph("graph_1", "project_1", "https://app.example.com")
	source := &model.ClientExecutionPackage{PackageID: "pkg_1", WorkflowGraph: graph}
	result := &model.RecordingResultPackage{
		ResultID:        "result_1",
		SourcePackageID: "other_pkg",
		SchemaVersion:   model.RecordingResultPackageSchemaVersion,
		Status:          model.RecordingResultStatusGenerated,
		ExecutionTrace:  &model.ExecutionTrace{WorkflowGraphID: graph.ID, StepResults: []model.StepResult{{NodeID: "start", Status: "passed"}}, Artifacts: []model.ArtifactRef{{ID: "a", URI: "file:///tmp/a.webm"}}},
	}

	if _, err := NewRenderRequestFromRecordingResult(source, result, "artifacts/render"); err == nil {
		t.Fatal("expected source package mismatch")
	}
}
