package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/experiment"
	"cascade-demoops/backend/internal/model"
)

type closedLoopEditorWorker struct{}

func (closedLoopEditorWorker) ProbeMedia(_ context.Context, request executor.MediaProbeRequest) (executor.MediaProbeResult, error) {
	return executor.MediaProbeResult{Path: request.Path, FileName: filepath.Base(request.Path), MimeType: "video/mp4", SHA256: fmt.Sprintf("probe-%s", filepath.Base(request.Path)), SizeBytes: 128, DurationMS: 70_000, Width: 1920, Height: 1080, FPS: 30, VideoCodec: "h264", AudioCodec: "aac", FFProbeAvailable: true}, nil
}

func (closedLoopEditorWorker) ValidateEditPlan(_ context.Context, request executor.EditPlanValidationRequest) (model.DemoEditPlanValidationReport, error) {
	return model.DemoEditPlanValidationReport{SchemaVersion: model.DemoEditPlanValidationSchemaVersion, PlanID: request.EditPlan.PlanID, Valid: len(request.Catalog.Steps) == 7 && len(request.EditPlan.Shots) == 7, CheckedAt: time.Now().UTC()}, nil
}

func (closedLoopEditorWorker) Render(context.Context, executor.RenderRequest) (executor.RenderResult, error) {
	return executor.RenderResult{}, nil
}

func TestClosedLoopEditorPreservesOnceOnlyChaptersAndUsesLatestRepairEvidence(t *testing.T) {
	service := newTestEditorService(t)
	service.editorWorker = closedLoopEditorWorker{}
	adapter := &appExperimentExecutionAdapter{service: service}
	root := t.TempDir()
	first := closedLoopFixtureBatch(t, root, "initial", model.RequiredDemoChapters())
	repair := closedLoopFixtureBatch(t, root, "repair", []string{"build_wait", "result_reveal", "interaction"})
	request := experiment.LegExecutionRequest{RunID: "experiment_v3", LegID: "main", ProjectName: "fresh-project", BuildPrompt: "构建一款适合产品演示的精致响应式网页游戏", HarnessProfile: experiment.HarnessProfileAdaptiveBusinessV2}
	materialized, err := adapter.buildClosedLoopEditorSession(t.Context(), request, []closedLoopCaptureBatch{first, repair})
	if err != nil {
		t.Fatal(err)
	}
	if !closedLoopEditorHasSevenChapters(materialized.Session) || len(materialized.Session.EditPlan.Shots) != 7 {
		t.Fatalf("closed-loop editor did not produce exactly seven facts: %+v", materialized.Session.AssetCatalog.Steps)
	}
	firstArtifact := materialized.Session.EditPlan.Shots[0].SourceArtifactID
	repairArtifact := materialized.Session.EditPlan.Shots[6].SourceArtifactID
	if firstArtifact == repairArtifact {
		t.Fatal("latest repair evidence was not selected for rerecordable chapters")
	}
	for index, shot := range materialized.Session.EditPlan.Shots {
		if len(shot.Overlays) != 0 {
			t.Fatalf("internal stage text leaked into chapter %d overlays: %+v", index, shot.Overlays)
		}
		if index < 4 && shot.SourceArtifactID != firstArtifact {
			t.Fatalf("once-only chapter %d was replaced by repair footage", index)
		}
		if index >= 4 && shot.SourceArtifactID != repairArtifact {
			t.Fatalf("rerecordable chapter %d did not use latest repair footage", index)
		}
	}
	again, err := adapter.buildClosedLoopEditorSession(t.Context(), request, []closedLoopCaptureBatch{first, repair})
	if err != nil || again.Session.SessionID != materialized.Session.SessionID {
		t.Fatalf("closed-loop editor materialization was not idempotent: first=%s second=%s err=%v", materialized.Session.SessionID, again.Session.SessionID, err)
	}
	coverage, facts, err := closedLoopNarrativeBoundary(request, materialized.Downloads, &materialized.Session)
	if err != nil || !coverage.Complete || len(facts) != 7 || facts[2].ApprovedCaptionVariants[0] != request.BuildPrompt {
		t.Fatalf("public narrative boundary is incomplete: coverage=%+v facts=%+v err=%v", coverage, facts, err)
	}
}

func TestClosedLoopManifestCompactsLongPreviewWaitWithoutOverlappingReveal(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "long-wait-segments.json")
	spans := []closedLoopChapterSpan{
		{Chapter: "login", SourceNodeID: "business_stage_session_setup", StartMS: 1_000, EndMS: 5_000},
		{Chapter: "submission", SourceNodeID: "business_stage_new_project_entry", StartMS: 6_000, EndMS: 8_000},
		{Chapter: "prompt_input", SourceNodeID: "business_stage_project_name_input", StartMS: 9_000, EndMS: 31_000},
		{Chapter: "creation", SourceNodeID: "business_stage_select_build_mode", StartMS: 32_000, EndMS: 35_000},
		{Chapter: "submission", SourceNodeID: "business_stage_start_agent_build", StartMS: 36_000, EndMS: 44_000},
		{Chapter: "result_reveal", SourceNodeID: "business_stage_contract_experiment_interaction_surface_ready", StartMS: 45_000, EndMS: 980_000},
		{Chapter: "interaction", SourceNodeID: "business_stage_contract_experiment_interaction_directional_moves", StartMS: 981_000, EndMS: 996_000},
		{Chapter: "build_wait", SourceNodeID: "business_stage_contract_experiment_interaction_merge_score", StartMS: 997_000, EndMS: 1_012_000},
		{Chapter: "result_reveal", SourceNodeID: "business_stage_contract_experiment_interaction_continued_stability", StartMS: 1_013_000, EndMS: 1_028_000},
		{Chapter: "interaction", SourceNodeID: "business_stage_contract_experiment_interaction_touch", StartMS: 1_029_000, EndMS: 1_044_000},
	}
	payload, _ := json.Marshal(map[string]any{"schema_version": "demoops.browser_recording_segments.v1", "rollover_ms": 120_000, "chapter_spans": spans})
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	selected, err := readClosedLoopChapterSpans([]CloudDeliverableDownloadResult{{Kind: "recording_segment_manifest", LocalPath: manifestPath}})
	if err != nil {
		t.Fatal(err)
	}
	if selected["creation"].SourceNodeID != "business_stage_new_project_entry" {
		t.Fatalf("creation was not recovered from the observed stage role: %+v", selected["creation"])
	}
	if selected["submission"].SourceNodeID != "business_stage_start_agent_build" {
		t.Fatalf("submission did not select the actual build action: %+v", selected["submission"])
	}
	interaction := selected["interaction"]
	if interaction.StartMS != 981_000 || interaction.EndMS != 1_044_000 {
		t.Fatalf("interaction proof session was not kept continuous: %+v", interaction)
	}
	waitStart, waitEnd, waitSpeed := closedLoopChapterSourceWindow("build_wait", selected["build_wait"], selected, 1_050_000)
	revealStart, revealEnd, revealSpeed := closedLoopChapterSourceWindow("result_reveal", selected["result_reveal"], selected, 1_050_000)
	if waitSpeed != 12 || waitEnd-waitStart != 96_000 {
		t.Fatalf("long wait was not deterministically compressed: range=%d..%d speed=%v", waitStart, waitEnd, waitSpeed)
	}
	if revealSpeed != 1 || revealEnd-revealStart != 8_000 || waitEnd != revealStart {
		t.Fatalf("stable reveal does not follow the wait window without overlap: wait_end=%d reveal=%d..%d", waitEnd, revealStart, revealEnd)
	}
}

func closedLoopFixtureBatch(t *testing.T, root, id string, chapters []string) closedLoopCaptureBatch {
	t.Helper()
	rawPath := filepath.Join(root, id+".mp4")
	if err := os.WriteFile(rawPath, []byte(id), 0o600); err != nil {
		t.Fatal(err)
	}
	spans := []closedLoopChapterSpan{}
	for index, chapter := range chapters {
		spans = append(spans, closedLoopChapterSpan{Chapter: chapter, StartMS: index * 8_000, EndMS: index*8_000 + 7_000})
	}
	manifestPath := filepath.Join(root, id+"-segments.json")
	payload, _ := json.Marshal(map[string]any{"schema_version": "demoops.browser_recording_segments.v1", "rollover_ms": 120_000, "chapter_spans": spans})
	if err := os.WriteFile(manifestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return closedLoopCaptureBatch{
		Result: model.RecordingResultPackage{ResultID: "result_" + id, CloudJobID: "job_" + id, SourcePackageID: "package_" + id, Status: model.RecordingResultStatusGenerated},
		Downloads: []CloudDeliverableDownloadResult{
			{ArtifactID: "raw_" + id, Kind: "raw_recording", LocalPath: rawPath, SizeBytes: int64(len(id)), ChecksumVerified: true},
			{ArtifactID: "manifest_" + id, Kind: "recording_segment_manifest", LocalPath: manifestPath, SizeBytes: int64(len(payload)), ChecksumVerified: true},
		},
	}
}
