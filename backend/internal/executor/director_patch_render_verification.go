package executor

import (
	"encoding/json"
	"os"
	"slices"
	"strings"

	"cascade-demoops/backend/internal/model"
)

func annotateDirectorPatchApplyRenderOutcome(result model.DirectorEditPlanPatchApplyResult, renderResult RenderResult) model.DirectorEditPlanPatchApplyResult {
	if strings.TrimSpace(renderResult.RenderManifestPath) == "" {
		return result
	}
	manifest, err := readDirectorPatchRenderManifest(renderResult.RenderManifestPath)
	if err != nil {
		result.Warnings = append(result.Warnings, editPlanPatchFinding("director_patch_render_manifest_unavailable", "could not read render manifest for director patch verification: "+err.Error(), "render_manifest_path"))
		return result
	}
	verification := model.DirectorPatchRenderVerification{
		Status:                  "satisfied",
		Method:                  manifest.Compositor.Method,
		QualityStatus:           manifest.Compositor.QualityStatus,
		FFMpegAvailable:         manifest.Compositor.FFMpegAvailable,
		FallbackReason:          manifest.Compositor.FallbackReason,
		RequirementReportStatus: manifest.RequirementSatisfactionReport.Status,
		PlannedOperations:       cloneStringSlice(manifest.Compositor.PlannedOperations),
		AppliedOperations:       cloneStringSlice(manifest.Compositor.AppliedOperations),
		SkippedOperations:       cloneSkippedOperations(manifest.Compositor.SkippedOperations),
	}
	verification.PendingOperationTypes = pendingRenderOperationTypes(verification.PlannedOperations, verification.AppliedOperations, verification.SkippedOperations)
	if len(verification.PendingOperationTypes) > 0 {
		verification.Status = "satisfied_with_pending_operations"
		result.Status = "applied_and_rerendered_with_pending_operations"
		for _, skipped := range verification.SkippedOperations {
			if !slices.Contains(verification.PendingOperationTypes, skipped.Type) {
				continue
			}
			message := "director patch requested render operation " + skipped.Type + ", but the current compositor did not render it into the final video"
			if strings.TrimSpace(skipped.Reason) != "" {
				message += ": " + skipped.Reason
			}
			result.Warnings = append(result.Warnings, editPlanPatchFinding("director_patch_operation_not_rendered", message, "render_manifest.compositor.skipped_operations."+skipped.Type))
		}
	}
	if verification.QualityStatus == "degraded" {
		result.Warnings = append(result.Warnings, editPlanPatchFinding("director_patch_rerender_degraded", "director patch was re-rendered, but the compositor reported degraded output quality", "render_manifest.compositor.quality_status"))
	}
	if strings.TrimSpace(verification.FallbackReason) != "" {
		result.Warnings = append(result.Warnings, editPlanPatchFinding("director_patch_rerender_fallback", "director patch re-render used fallback path: "+verification.FallbackReason, "render_manifest.compositor.fallback_reason"))
	}
	switch verification.RequirementReportStatus {
	case "not_satisfied":
		verification.Status = "not_satisfied"
		result.Status = "applied_and_rerendered_with_requirement_failures"
		result.Warnings = append(result.Warnings, editPlanPatchFinding("director_patch_requirement_report_not_satisfied", "requirement satisfaction report still has blocking errors after director patch re-render", "render_manifest.requirement_satisfaction_report.status"))
	case "satisfied_with_warnings":
		if verification.Status == "satisfied" {
			verification.Status = "satisfied_with_warnings"
		}
		result.Warnings = append(result.Warnings, editPlanPatchFinding("director_patch_requirement_report_warnings", "requirement satisfaction report still has warnings after director patch re-render", "render_manifest.requirement_satisfaction_report.status"))
	}
	result.RenderVerification = &verification
	return result
}

func readDirectorPatchRenderManifest(path string) (directorPatchRenderManifest, error) {
	var manifest directorPatchRenderManifest
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func pendingRenderOperationTypes(planned []string, applied []string, skipped []model.DirectorPatchSkippedOperation) []string {
	pending := []string{}
	for _, operation := range planned {
		operation = strings.TrimSpace(operation)
		if operation == "" || slices.Contains(applied, operation) {
			continue
		}
		if len(skipped) > 0 && !skippedOperationContains(skipped, operation) {
			continue
		}
		if !slices.Contains(pending, operation) {
			pending = append(pending, operation)
		}
	}
	return pending
}

func skippedOperationContains(skipped []model.DirectorPatchSkippedOperation, operation string) bool {
	for _, skippedOperation := range skipped {
		if skippedOperation.Type == operation {
			return true
		}
	}
	return false
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func cloneSkippedOperations(values []model.DirectorPatchSkippedOperation) []model.DirectorPatchSkippedOperation {
	if len(values) == 0 {
		return nil
	}
	out := make([]model.DirectorPatchSkippedOperation, len(values))
	copy(out, values)
	return out
}

type directorPatchRenderManifest struct {
	Compositor                    directorPatchRenderCompositor `json:"compositor"`
	RequirementSatisfactionReport struct {
		Status string `json:"status"`
	} `json:"requirement_satisfaction_report"`
}

type directorPatchRenderCompositor struct {
	Method            string                                `json:"method"`
	QualityStatus     string                                `json:"quality_status"`
	FFMpegAvailable   *bool                                 `json:"ffmpeg_available"`
	FallbackReason    string                                `json:"fallback_reason"`
	PlannedOperations []string                              `json:"planned_operations"`
	AppliedOperations []string                              `json:"applied_operations"`
	SkippedOperations []model.DirectorPatchSkippedOperation `json:"skipped_operations"`
}
