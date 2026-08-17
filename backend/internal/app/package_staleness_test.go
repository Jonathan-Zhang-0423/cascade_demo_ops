package app

import (
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

func TestPackageGenerationLineageMarksNewerEvidenceAsStale(t *testing.T) {
	planAt := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	pageAt := planAt.Add(2 * time.Hour)
	state := &orchestrator.CascadeState{
		ProjectContext:         &model.ProjectContext{UpdatedAt: planAt.Add(-time.Hour)},
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{CreatedAt: planAt, UpdatedAt: planAt},
		CodeSnapshots:          []model.CodeUnderstandingSnapshot{{CreatedAt: planAt.Add(-time.Minute)}},
		PageSnapshots:          []model.PageUnderstandingSnapshot{{CapturedAt: pageAt}},
	}
	metadata := packageGenerationLineageMetadata(state)
	if metadata["staleness_status"] != "inputs_newer_than_plan" || metadata["regeneration_required"] != true {
		t.Fatalf("newer page evidence must make the old plan visibly stale: %+v", metadata)
	}
	if finding := preflightPackageStaleness(state); finding == nil || finding.ID != "execution_plan_stale" || finding.Severity != model.FindingSeverityBlocking {
		t.Fatalf("stale input lineage must block approval: %+v", finding)
	}
}

func TestPackageGenerationLineageExposesFreshSnapshotTimes(t *testing.T) {
	pageAt := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	planAt := pageAt.Add(time.Hour)
	state := &orchestrator.CascadeState{
		ProjectContext:         &model.ProjectContext{UpdatedAt: pageAt},
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{CreatedAt: planAt, UpdatedAt: planAt},
		PageSnapshots:          []model.PageUnderstandingSnapshot{{CapturedAt: pageAt}},
	}
	metadata := packageGenerationLineageMetadata(state)
	if metadata["staleness_status"] != "current" || metadata["plan_generated_at"] == nil || metadata["page_scan_at"] == nil {
		t.Fatalf("fresh package lineage should expose audit timestamps: %+v", metadata)
	}
	if finding := preflightPackageStaleness(state); finding != nil {
		t.Fatalf("fresh lineage must not be blocked: %+v", finding)
	}
}
