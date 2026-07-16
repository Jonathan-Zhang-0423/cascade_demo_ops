package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestMarkDirectorPatchApplyRerenderedReportsPendingCaptionBurnIn(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "render_manifest.json")
	writeRenderManifest(t, manifestPath, `{
  "schema_version": "demoops.render_manifest.v1",
  "compositor": {
    "method": "copy_source_recording",
    "quality_status": "degraded",
    "ffmpeg_available": false,
    "fallback_reason": "ffmpeg_not_available",
    "planned_operations": ["caption", "trim"],
    "applied_operations": ["fallback_copy"],
    "skipped_operations": [
      {"type": "caption", "reason": "current deterministic compositor does not burn this operation into pixels yet"},
      {"type": "trim", "reason": "ffmpeg trim/concat was not executed in fallback mode"}
    ]
  },
  "requirement_satisfaction_report": {
    "status": "satisfied_with_warnings"
  }
}`)
	result := model.DirectorEditPlanPatchApplyResult{
		SchemaVersion:     model.DirectorEditPlanPatchApplyResultSchemaVersion,
		ResultID:          "apply_1",
		CreatedAt:         time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Applied:           true,
		RerenderRequested: true,
	}

	got := MarkDirectorPatchApplyRerendered(result, RenderResult{
		VideoPath:          "artifacts/render/demo_12s.webm",
		DemoEditPlanPath:   "artifacts/render/demo_edit_plan.json",
		RenderManifestPath: manifestPath,
	})

	if got.Status != "applied_and_rerendered_with_pending_operations" || !got.Rerendered {
		t.Fatalf("expected rerendered result with pending operation status, got %+v", got)
	}
	if got.RenderVerification == nil {
		t.Fatalf("expected render verification to be attached: %+v", got)
	}
	if !containsString(got.RenderVerification.PendingOperationTypes, "caption") {
		t.Fatalf("expected caption burn-in to be pending: %+v", got.RenderVerification)
	}
	warningCodes := validationFindingCodes(got.Warnings)
	for _, want := range []string{"director_patch_operation_not_rendered", "director_patch_rerender_degraded", "director_patch_rerender_fallback", "director_patch_requirement_report_warnings"} {
		if !strings.Contains(warningCodes, want) {
			t.Fatalf("expected warning %q, got %+v", want, got.Warnings)
		}
	}
}

func TestMarkDirectorPatchApplyRerenderedAcceptsAppliedCaption(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "render_manifest.json")
	writeRenderManifest(t, manifestPath, `{
  "schema_version": "demoops.render_manifest.v1",
  "compositor": {
    "method": "ffmpeg_trim_concat",
    "quality_status": "ok",
    "ffmpeg_available": true,
    "planned_operations": ["caption", "trim"],
    "applied_operations": ["caption", "trim"],
    "skipped_operations": []
  },
  "requirement_satisfaction_report": {
    "status": "satisfied"
  }
}`)
	result := model.DirectorEditPlanPatchApplyResult{
		SchemaVersion:     model.DirectorEditPlanPatchApplyResultSchemaVersion,
		ResultID:          "apply_1",
		CreatedAt:         time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Applied:           true,
		RerenderRequested: true,
	}

	got := MarkDirectorPatchApplyRerendered(result, RenderResult{
		VideoPath:          "artifacts/render/demo_12s.webm",
		DemoEditPlanPath:   "artifacts/render/demo_edit_plan.json",
		RenderManifestPath: manifestPath,
	})

	if got.Status != "applied_and_rerendered" || !got.Rerendered {
		t.Fatalf("expected clean rerendered status, got %+v", got)
	}
	if got.RenderVerification == nil || got.RenderVerification.Status != "satisfied" {
		t.Fatalf("expected satisfied render verification, got %+v", got.RenderVerification)
	}
	if len(got.RenderVerification.PendingOperationTypes) != 0 {
		t.Fatalf("did not expect pending operations: %+v", got.RenderVerification)
	}
	if strings.Contains(validationFindingCodes(got.Warnings), "director_patch_operation_not_rendered") {
		t.Fatalf("did not expect operation warning: %+v", got.Warnings)
	}
}

func writeRenderManifest(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
