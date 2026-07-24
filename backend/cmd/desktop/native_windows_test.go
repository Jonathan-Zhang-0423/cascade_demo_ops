//go:build windows

package main

import (
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

func TestNativeApprovalChecklistTextSummarizesBundleWithoutSecrets(t *testing.T) {
	bundle := &model.ExecutableRecordingScriptBundle{
		ProjectID: "project_native_checklist",
		ScriptManifest: model.ExecutableScriptManifest{
			Runtime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
		},
		ApprovalMarkdown: model.ApprovalMarkdownDocument{
			SHA256: "sha_markdown",
		},
		StageApprovalPlan: &model.StageApprovalPlan{
			Stages: []model.StageApprovalStage{
				{
					ID:            "stage_create_project",
					Order:         1,
					Title:         "新建项目",
					TargetRoute:   "/projects/new",
					Interaction:   model.BrowserAgentInteraction{Kind: model.GraphActionClick, SecretRef: "secret://demo/password"},
					SuccessState:  "进入项目详情页",
					ComponentRefs: []string{"component:new-project-button"},
				},
			},
		},
		ScriptOutline: &model.BrowserAgentScriptOutline{
			Stages: []model.BrowserAgentOutlineStage{
				{StageID: "stage_create_project", Order: 1, Route: "/projects/new"},
			},
			AllowedExplorationScope: model.BrowserAgentExplorationScope{
				AllowedOrigins: []string{"https://example.test"},
			},
			ForbiddenActions: []string{"delete", "purchase"},
		},
		Reproducibility: model.ExecutableScriptReproducibility{
			BundleHashSHA256:    "sha_bundle",
			StagePlanHashSHA256: "sha_stage",
			OutlineHashSHA256:   "sha_outline",
		},
		Validation: &model.ExecutableScriptValidation{Valid: true},
	}

	text := nativeApprovalChecklistText(&orchestrator.CascadeState{ProjectID: bundle.ProjectID}, bundle)
	for _, expected := range []string{
		"[OK] Stage plan 包含 1 个阶段",
		"[OK] Script outline 包含 1 个阶段",
		"路由/状态已声明: /projects/new",
		"动作语义: click",
		"证据链已绑定",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected checklist to contain %q:\n%s", expected, text)
		}
	}
	for _, forbidden := range []string{"secret://demo/password", "000000"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("checklist leaked sensitive value %q:\n%s", forbidden, text)
		}
	}
}

func TestNativeApprovalChecklistTextMarksCoverageMismatchForReview(t *testing.T) {
	bundle := &model.ExecutableRecordingScriptBundle{
		ProjectID: "project_native_review",
		ScriptManifest: model.ExecutableScriptManifest{
			Runtime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
		},
		StageApprovalPlan: &model.StageApprovalPlan{
			Stages: []model.StageApprovalStage{{ID: "stage_one", Order: 1}},
		},
		ScriptOutline: &model.BrowserAgentScriptOutline{},
	}

	text := nativeApprovalChecklistText(&orchestrator.CascadeState{ProjectID: bundle.ProjectID}, bundle)
	if !strings.Contains(text, "[REVIEW] 阶段数量不一致：stage=1 outline=0") {
		t.Fatalf("expected mismatch review finding:\n%s", text)
	}
	if !strings.Contains(text, "[REVIEW] validation missing") {
		t.Fatalf("expected missing validation review finding:\n%s", text)
	}
}
