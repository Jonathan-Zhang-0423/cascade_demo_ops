package executor

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

func TestNewDirectorInputPreservesSourceOnlyBoundary(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	recording := model.RecordingResultPackage{
		ResultID:        "result_1",
		SourcePackageID: source.PackageID,
		ExecutionTrace: &model.ExecutionTrace{
			ID:              "trace_1",
			WorkflowGraphID: source.WorkflowGraph.ID,
			GraphVersion:    source.WorkflowGraph.Version,
			PassRate:        1,
			StepResults: []model.StepResult{{
				NodeID:        "node_start",
				Status:        "passed",
				DurationMS:    1200,
				ObservedState: "Dashboard loaded",
			}},
			Artifacts: []model.ArtifactRef{{
				ID:           "artifact_raw_recording",
				Kind:         "raw_recording",
				URI:          "artifacts/recording/raw.webm",
				MimeType:     "video/webm",
				SourceNodeID: "node_start",
				Metadata:     map[string]any{"include_in_demo": true, "asset_role": "raw_recording"},
			}},
		},
		GeneratedAssets: []model.ArtifactRef{{
			ID:           "artifact_screenshot",
			Kind:         "screenshot",
			URI:          "artifacts/recording/step-001.png",
			MimeType:     "image/png",
			SourceNodeID: "node_start",
			Metadata:     map[string]any{"include_in_demo": true, "asset_role": "primary"},
		}},
	}
	renderResult := RenderResult{
		VideoPath:                "artifacts/render/final.mp4",
		SourceReferenceVideoPath: "artifacts/render/source_reference.mp4",
		DemoEditPlan: &model.DemoEditPlan{
			PlanID:               "plan_1",
			SourceAuthority:      model.DemoEditSourceAuthorityCustomerSideAgent,
			ModelRole:            model.DemoEditModelRolePresentationOptimizerOnly,
			SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly,
			ScriptOrderPolicy:    model.DemoEditScriptOrderPolicyPreserveRequiredStepOrder,
		},
	}

	input := NewDirectorInput(&source, &recording, renderResult, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))

	if input.SchemaVersion != model.DirectorInputSchemaVersion {
		t.Fatalf("schema version = %q", input.SchemaVersion)
	}
	if input.SourceMaterialPolicy != model.DemoEditSourceMaterialPolicyExistingAssetsOnly || input.ModelRole != model.DemoEditModelRolePresentationOptimizerOnly {
		t.Fatalf("director input lost collaboration boundary: %+v", input)
	}
	if input.UserIntent.InputMode != "full_auto_authorized" || !input.UserIntent.HighestPriority {
		t.Fatalf("automatic route should preserve highest-priority user intent policy without manual prompt: %+v", input.UserIntent)
	}
	if len(input.DecisionPriority.ResolutionOrder) != 3 || input.DecisionPriority.ResolutionOrder[0].Source != "user_explicit_requirements" {
		t.Fatalf("director input must encode UserIntent > ClientScript > ModelSuggestion priority: %+v", input.DecisionPriority)
	}
	if !input.StorylinePolicy.PreserveStepOrder || len(input.StorylinePolicy.RequiredStepOrder) == 0 || input.StorylinePolicy.RequiredStepOrder[0] != "node_start" {
		t.Fatalf("director input must preserve script step order: %+v", input.StorylinePolicy)
	}
	cannotDo := strings.Join(input.ModelBoundaries.CannotDo, "\n")
	if !strings.Contains(cannotDo, "generate or replace product UI") || !strings.Contains(cannotDo, "create new browser actions") {
		t.Fatalf("director boundaries do not forbid UI hallucination or action changes: %+v", input.ModelBoundaries)
	}
	if input.Materials.SourceReferenceVideo == nil || input.Materials.SourceReferenceVideo.MimeType != "video/mp4" {
		t.Fatalf("source reference video was not promoted for model input: %+v", input.Materials)
	}
	if len(input.Materials.Screenshots) != 1 || input.Materials.Screenshots[0].SourceNodeID != "node_start" {
		t.Fatalf("screenshots were not preserved as source material: %+v", input.Materials.Screenshots)
	}
}

func TestServerDirectorRuntimeAllowsOnlyDoubaoFamilyProviders(t *testing.T) {
	t.Setenv("GLM_API_KEY", "must-not-enter-server-runtime")
	t.Setenv("KIMI_API_KEY", "must-not-enter-server-runtime")
	t.Setenv("MINIMAX_API_KEY", "must-not-enter-server-runtime")
	t.Setenv("DEEPSEEK_API_KEY", "must-not-enter-server-runtime")
	t.Setenv("DOUBAO_API_KEY", "doubao-test-key")

	runtime := arkMediaRuntimeFromEnv(config.ArkMediaModeReal)
	if len(runtime.ModelProviders) != 3 {
		t.Fatalf("server director providers = %v, want exactly doubao/seedance/seedream", runtime.ModelProviders)
	}
	for _, provider := range []config.ModelProvider{
		config.ModelProviderDoubao,
		config.ModelProviderSeedance,
		config.ModelProviderSeedream,
	} {
		if _, ok := runtime.ModelProviders[provider]; !ok {
			t.Fatalf("server director runtime is missing allowed provider %s", provider)
		}
	}
	for _, provider := range []config.ModelProvider{
		config.ModelProviderGLM,
		config.ModelProviderKimi,
		config.ModelProviderMinimax,
		config.ModelProviderDeepSeek,
	} {
		if _, ok := runtime.ModelProviders[provider]; ok {
			t.Fatalf("forbidden provider %s entered the server director runtime", provider)
		}
	}
}

func TestServerDirectorRuntimeRejectsNonDoubaoModelOverrides(t *testing.T) {
	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousWriter) })

	t.Setenv("SEEDANCE_MODEL", "glm-5")
	t.Setenv("SEEDREAM_MODEL", "deepseek-image")
	t.Setenv("DOUBAO_MODEL", "kimi-k2.7-code")

	runtime := arkMediaRuntimeFromEnv(config.ArkMediaModeReal)
	if got := runtime.ModelProviders[config.ModelProviderSeedance].DefaultModel; got != defaultSeedanceModel {
		t.Fatalf("seedance model = %q, want safe default %q", got, defaultSeedanceModel)
	}
	if got := runtime.ModelProviders[config.ModelProviderSeedream].DefaultModel; got != defaultSeedreamModel {
		t.Fatalf("seedream model = %q, want safe default %q", got, defaultSeedreamModel)
	}
	if got := runtime.ModelProviders[config.ModelProviderDoubao].DefaultModel; got != "" {
		t.Fatalf("doubao model = %q, want empty safe default", got)
	}
	for _, envName := range []string{"SEEDANCE_MODEL", "SEEDREAM_MODEL", "DOUBAO_MODEL"} {
		if !strings.Contains(logs.String(), "model_config_rejected env="+envName) {
			t.Fatalf("missing explicit configuration warning for %s: %s", envName, logs.String())
		}
	}
}

func TestNewDirectorInputCapturesVerifiedInteractionAndRuntimeAdaptiveContext(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	source.WorkflowGraph.Nodes[0].ID = "node_verified"
	source.WorkflowGraph.Nodes[0].Title = "Invite teammate"
	source.WorkflowGraph.Nodes[0].Goal = "Show a verified business action."
	source.WorkflowGraph.Nodes[0].ExpectedOutcome = "Invitation action is available"
	source.WorkflowGraph.Nodes[0].ActionSpec = &model.GraphAction{
		Type: model.GraphActionClick,
		Target: model.ActionTarget{
			Selector: "[data-testid='invite-user']",
			Label:    "Invite teammate",
			SelectorAlternatives: []model.SelectorCandidate{{
				Kind:           "css",
				Value:          "button:has-text('Invite')",
				Source:         "playwright_readonly_scan",
				Confidence:     0.9,
				StabilityScore: 0.8,
			}},
		},
		TimeoutMS: 12000,
	}
	source.WorkflowGraph.Nodes[0].Narrative = &model.NarrativeCue{Caption: "Invite a teammate instantly.", Callout: "Invite"}
	source.WorkflowGraph.Nodes[0].Capture = &model.CaptureSpec{Screenshot: true, Video: true, Zoom: true, FullPage: true, FocusSelector: "[data-testid='invite-user']", AssetRole: "verified_business_action"}
	source.WorkflowGraph.Nodes[0].EvidenceRefs = []model.EvidenceRef{{ID: "ev_verified", Kind: model.EvidenceKindBrowserScan}}
	source.WorkflowGraph.Nodes[0].Metadata = map[string]any{
		"verified_interaction_id": "verified_invite",
		"intent_goal_id":          "intent_invite",
		"verification_status":     "verified",
		"verification_source":     "playwright_readonly_scan",
		"selector_score":          96,
		"runtime_adaptive":        false,
	}
	source.WorkflowGraph.Nodes = append(source.WorkflowGraph.Nodes, &model.GraphNode{
		ID:              "node_adaptive",
		Type:            model.GraphNodeTypeAction,
		Title:           "Find generated action",
		ExpectedOutcome: "Runtime adaptive action may resolve on the live page",
		ActionSpec: &model.GraphAction{
			Type: model.GraphActionClick,
			Target: model.ActionTarget{
				Selector: "button:has-text('Generate')",
				Label:    "Generate",
				SelectorAlternatives: []model.SelectorCandidate{{
					Kind:   "css",
					Value:  "[role='button']:has-text('Generate')",
					Source: "runtime_adaptive",
				}},
			},
			TimeoutMS: 12000,
		},
		Capture:        &model.CaptureSpec{Screenshot: true, Video: true, Zoom: true, FocusSelector: "button:has-text('Generate')", AssetRole: "adaptive_business_action"},
		DurationHintMS: 12000,
		Metadata: map[string]any{
			"verified_interaction_id": "adaptive_generate",
			"intent_goal_id":          "intent_generate",
			"verification_status":     "runtime_adaptive",
			"verification_source":     "runtime_adaptive_discovery",
			"selector_score":          42,
			"runtime_adaptive":        true,
		},
	})
	source.RecordingRunSpec.Timeline.NodeTimingHints = []model.NodeTimingHint{
		{NodeID: "node_verified", DurationMS: 14000, HoldAfterMS: 5000},
		{NodeID: "node_adaptive", DurationMS: 12000, HoldAfterMS: 4000},
	}
	source.RecordingRunSpec.Timeline.CaptureWindows = []model.CaptureWindow{
		{ID: "capture_verified", NodeID: "node_verified", StartMS: 0, DurationMS: 14000, Role: "verified_business_action"},
		{ID: "capture_adaptive", NodeID: "node_adaptive", StartMS: 14000, DurationMS: 12000, Role: "adaptive_business_action"},
	}
	recording := model.RecordingResultPackage{ResultID: "result_verified", SourcePackageID: source.PackageID}

	input := NewDirectorInput(&source, &recording, RenderResult{}, time.Date(2026, 7, 15, 22, 30, 0, 0, time.UTC))

	if len(input.Workflow.Nodes) != 2 {
		t.Fatalf("expected verified and runtime-adaptive workflow nodes, got %+v", input.Workflow.Nodes)
	}
	verified := input.Workflow.Nodes[0]
	if verified.Verification == nil || verified.Verification.Authority != "verified_product_fact" || verified.Verification.SelectorScore != 96 {
		t.Fatalf("verified interaction context was not preserved: %+v", verified.Verification)
	}
	if verified.Capture == nil || !verified.Capture.FullPage || verified.Capture.FocusSelector != "[data-testid='invite-user']" {
		t.Fatalf("capture instructions should be preserved for director input: %+v", verified.Capture)
	}
	if verified.Timing == nil || verified.Timing.DurationMS != 14000 || verified.CaptureWindow == nil || verified.CaptureWindow.Role != "verified_business_action" {
		t.Fatalf("timing/capture window should come from recording_run_spec: timing=%+v window=%+v", verified.Timing, verified.CaptureWindow)
	}
	if len(verified.Target.SelectorAlternatives) != 1 || verified.Target.SelectorAlternatives[0].Source != "playwright_readonly_scan" {
		t.Fatalf("selector alternatives should be retained for model context: %+v", verified.Target.SelectorAlternatives)
	}
	adaptive := input.Workflow.Nodes[1]
	if adaptive.Verification == nil || !adaptive.Verification.RuntimeAdaptive || adaptive.Verification.Authority != "runtime_adaptive_executable_intent" {
		t.Fatalf("runtime-adaptive context should be marked as executable intent, not verified fact: %+v", adaptive.Verification)
	}
	if adaptive.Required {
		t.Fatalf("runtime-adaptive steps should not be marked as fully required product facts for director planning: %+v", adaptive)
	}
	if len(input.Recording.NodeTimingHints) != 2 || len(input.Recording.CaptureWindows) != 2 || len(input.Recording.BrowserViewports) == 0 {
		t.Fatalf("recording timing, capture windows, and viewports should be preserved: %+v", input.Recording)
	}
	questions := strings.Join(input.OpenQuestions, "\n")
	if !strings.Contains(questions, "runtime-adaptive") || !strings.Contains(questions, "low selector confidence") {
		t.Fatalf("open questions should warn the director about adaptive/low-confidence steps: %s", questions)
	}
	prompt := seedanceDirectorPrompt(input)
	if !strings.Contains(prompt, "runtime-adaptive steps") {
		t.Fatalf("Seedance prompt should warn about runtime-adaptive steps: %s", prompt)
	}
}

func TestNewDirectorInputPrioritizesManualUserIntentFromMetadata(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	source.ProjectContextSummary.Goals = []model.DemoGoal{{
		ID:               "goal_roi",
		ValueProposition: "Show measurable ROI from the dashboard workflow.",
		SuccessCriteria:  []string{"Make the business value clear before the closing shot."},
		Priority:         9,
	}}
	source.WorkflowGraph.Intent = &model.WorkflowIntent{
		Objective:        "Demonstrate the dashboard workflow.",
		ValueProposition: "Show operational visibility.",
		SuccessCriteria:  []string{"Dashboard appears ready for sales review."},
	}
	source.WorkflowGraph.Requirements = []model.GraphRequirement{{
		ID:          "graph_req_analytics",
		Kind:        "must_show",
		Description: "Show dashboard analytics after navigation.",
		Required:    true,
		NodeRefs:    []string{"node_start"},
	}}
	source.Metadata = map[string]any{
		"user_demo_intent": map[string]any{
			"input_mode":          "manual_prompt",
			"raw_prompt":          "Prioritize the approval workflow and make the final video feel enterprise-ready.",
			"target_audience":     "enterprise buyer",
			"target_duration_sec": 45,
			"must_show":           []any{"Approval status changes from pending to approved."},
			"must_not_show":       []any{"Internal debugging pages."},
			"style":               "Polished enterprise launch video.",
			"captions":            []any{"Approval completed in seconds."},
			"requirements": []any{
				map[string]any{
					"id":       "req_voiceover_roi",
					"kind":     "voiceover",
					"text":     "Voiceover must mention reduced manual review effort.",
					"required": true,
					"priority": 990,
				},
			},
		},
	}
	recording := model.RecordingResultPackage{ResultID: "result_manual", SourcePackageID: source.PackageID}
	renderResult := RenderResult{SourceReferenceVideoPath: "artifacts/render/source_reference.mp4"}

	input := NewDirectorInput(&source, &recording, renderResult, time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC))

	if input.UserIntent.Status != "manual_requirements_present" || input.UserIntent.InputMode != "manual_prompt" {
		t.Fatalf("manual metadata should become highest-priority user intent: %+v", input.UserIntent)
	}
	if input.UserIntent.TargetAudience != "enterprise buyer" || input.UserIntent.TargetDurationSec != 45 {
		t.Fatalf("manual audience/duration should override package defaults: %+v", input.UserIntent)
	}
	if !hasDirectorRequirement(input.UserIntent.Requirements, "must_show", "Approval status changes", "user_explicit_metadata") {
		t.Fatalf("manual must-show requirement missing or not traced: %+v", input.UserIntent.Requirements)
	}
	if !hasDirectorRequirement(input.UserIntent.Requirements, "must_not_show", "Internal debugging pages", "user_explicit_metadata") {
		t.Fatalf("manual must-not-show requirement missing or not traced: %+v", input.UserIntent.Requirements)
	}
	if !hasDirectorRequirement(input.UserIntent.Requirements, "must_show", "Show dashboard analytics", "client_workflow_graph_requirement") {
		t.Fatalf("client-side normalized requirements should still be preserved below manual intent: %+v", input.UserIntent.Requirements)
	}
	if input.UserIntent.Requirements[0].Source != "user_explicit_metadata" {
		t.Fatalf("manual requirements should be listed before script-normalized requirements: %+v", input.UserIntent.Requirements)
	}
	content := seedanceContentPreview(input)
	text, _ := content[0]["text"].(string)
	if !strings.Contains(text, "User requirements have highest priority") || !strings.Contains(text, "Approval status changes") || !strings.Contains(text, "Preserve required step order") {
		t.Fatalf("Seedance director preview prompt must carry priority, user intent, and script order: %s", text)
	}
}

func TestNewArkMediaDryRunPlanUsesArkV3AndNoRealCall(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	input := model.DirectorInput{
		SourcePackageID: source.PackageID,
		Project:         model.DirectorProjectContext{ProductName: "Cascade"},
		Materials: model.DirectorMaterialSet{
			SourceReferenceVideo: &model.DirectorMaterialRef{ID: "source_reference", Kind: "source_reference_video", URI: "artifacts/render/source_reference.mp4", MimeType: "video/mp4"},
			Screenshots:          []model.DirectorMaterialRef{{ID: "shot_1", Kind: "screenshot", URI: "artifacts/recording/step-001.png", MimeType: "image/png"}},
		},
	}
	directorRef := model.DirectorMaterialRef{ID: "director_input", Kind: "director_input", URI: "artifacts/render/director_input.json", MimeType: "application/json"}

	plan := NewArkMediaDryRunPlan(&source, directorRef, input, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))

	if plan.Mode != "dry_run" || plan.VideoModel != "doubao-seedance-2-0-260128" {
		t.Fatalf("unexpected dry-run plan identity: %+v", plan)
	}
	if len(plan.RecommendedTasks) != 2 {
		t.Fatalf("expected Seedance and Seedream dry-run tasks, got %+v", plan.RecommendedTasks)
	}
	if !strings.Contains(plan.RecommendedTasks[0].Endpoint, "/api/v3/contents/generations/tasks") {
		t.Fatalf("Seedance endpoint is not Ark v3 content generation: %+v", plan.RecommendedTasks[0])
	}
	if bodyModel, _ := plan.RecommendedTasks[0].RequestBody["model"].(string); bodyModel != plan.VideoModel {
		t.Fatalf("Seedance request body model mismatch: %+v", plan.RecommendedTasks[0].RequestBody)
	}
	if len(plan.RequiredBeforeRealCall) == 0 || !strings.Contains(strings.Join(plan.RequiredBeforeRealCall, "\n"), "download returned") {
		t.Fatalf("dry-run plan must state real-call prerequisites: %+v", plan.RequiredBeforeRealCall)
	}
	if plan.ProviderConstraints.Seedance.Model != "doubao-seedance-2-0-260128" || plan.ProviderConstraints.Seedance.MaxDurationSec != 15 {
		t.Fatalf("dry-run plan must expose Seedance constraints: %+v", plan.ProviderConstraints)
	}
	if len(plan.SourceAssetRequirements) == 0 {
		t.Fatalf("dry-run plan must expose source asset requirements: %+v", plan)
	}
	if plan.RealCallReadiness.CanCallNow || plan.RealCallReadiness.CanCallWhenEnabled {
		t.Fatalf("local dry-run source assets must not be marked callable: %+v", plan.RealCallReadiness)
	}
	if len(plan.RealCallReadiness.Blockers) == 0 || plan.RealCallReadiness.Blockers[0].Code != "needs_public_uri" {
		t.Fatalf("expected local source reference to block real Ark calls: %+v", plan.RealCallReadiness)
	}
	if !plan.OutputHandling.MustNotReplaceCapturedUI || plan.OutputHandling.ExpectedOutputs[0].IncludeInDemo {
		t.Fatalf("Ark output handling must keep generated candidates non-authoritative: %+v", plan.OutputHandling)
	}
}

func hasDirectorRequirement(requirements []model.DirectorUserRequirement, kind string, containsText string, source string) bool {
	for _, requirement := range requirements {
		if requirement.Kind == kind && requirement.Source == source && strings.Contains(requirement.Text, containsText) {
			return true
		}
	}
	return false
}

func TestNewArkMediaDryRunPlanReportsReadyWhenSourceAssetsArePublic(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	input := model.DirectorInput{
		SourcePackageID: source.PackageID,
		Project:         model.DirectorProjectContext{ProductName: "Cascade"},
		Materials: model.DirectorMaterialSet{
			SourceReferenceVideo: &model.DirectorMaterialRef{
				ID:       "source_reference",
				Kind:     "source_reference_video",
				URI:      "https://assets.example.com/source_reference.mp4",
				MimeType: "video/mp4",
			},
			Screenshots: []model.DirectorMaterialRef{{
				ID:       "shot_1",
				Kind:     "screenshot",
				URI:      "https://assets.example.com/step-001.png",
				MimeType: "image/png",
			}},
		},
	}
	directorRef := model.DirectorMaterialRef{ID: "director_input", Kind: "director_input", URI: "https://assets.example.com/director_input.json", MimeType: "application/json"}

	plan := NewArkMediaDryRunPlan(&source, directorRef, input, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))

	if plan.RealCallReadiness.Status != "ready_when_enabled" || plan.RealCallReadiness.CanCallNow || !plan.RealCallReadiness.CanCallWhenEnabled {
		t.Fatalf("public source assets should be ready only after mode gate is enabled: %+v", plan.RealCallReadiness)
	}
	if len(plan.RealCallReadiness.Blockers) != 0 {
		t.Fatalf("did not expect blockers for public source assets: %+v", plan.RealCallReadiness.Blockers)
	}
	for _, requirement := range plan.SourceAssetRequirements {
		if requirement.Required && requirement.Status != "ready" {
			t.Fatalf("required public asset should be ready: %+v", requirement)
		}
	}
}

func TestNewArkAssetPublicationPlanMarksLocalAssetsForPublication(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	arkPlan := model.ArkMediaDryRunPlan{
		SourcePackageID: source.PackageID,
		SourceAssetRequirements: []model.ArkMediaSourceAssetRequirement{{
			Ref:               model.DirectorMaterialRef{ID: "source_reference", Kind: "source_reference_video", URI: "artifacts/render/source_reference.mp4", MimeType: "video/mp4"},
			TaskID:            "seedance_reference_director_preview",
			Usage:             "required_reference_video",
			Required:          true,
			AcceptedMimeTypes: []string{"video/mp4", "video/quicktime"},
			RequiresPublicURI: true,
			Status:            "needs_public_uri",
		}, {
			Ref:               model.DirectorMaterialRef{ID: "shot_1", Kind: "screenshot", URI: "artifacts/recording/step-001.png", MimeType: "image/png"},
			TaskID:            "seedance_reference_director_preview",
			Usage:             "reference_video_or_image",
			AcceptedMimeTypes: []string{"image/png", "image/jpeg"},
			RequiresPublicURI: true,
			Status:            "needs_public_uri",
		}, {
			Ref:               model.DirectorMaterialRef{ID: "shot_1", Kind: "screenshot", URI: "artifacts/recording/step-001.png", MimeType: "image/png"},
			TaskID:            "seedream_non_product_visuals",
			Usage:             "optional_style_reference_image",
			AcceptedMimeTypes: []string{"image/png", "image/jpeg"},
			RequiresPublicURI: true,
			Status:            "needs_public_uri",
		}},
	}
	arkPlanRef := model.DirectorMaterialRef{ID: "ark_plan", Kind: "ark_media_dry_run_plan", URI: "artifacts/render/ark_media_dry_run_plan.json", MimeType: "application/json"}

	plan := NewArkAssetPublicationPlan(&source, arkPlanRef, arkPlan, time.Date(2026, 7, 14, 12, 30, 0, 0, time.UTC))

	if plan.SchemaVersion != model.ArkAssetPublicationPlanSchemaVersion || plan.Status != "needs_publication" {
		t.Fatalf("unexpected publication plan identity/status: %+v", plan)
	}
	if len(plan.Blockers) != 0 {
		t.Fatalf("local supported assets should need publication, not block: %+v", plan.Blockers)
	}
	if len(plan.Items) != 2 {
		t.Fatalf("duplicate screenshot requirements should be merged by asset, got %+v", plan.Items)
	}
	sourceItem := plan.Items[0]
	if !sourceItem.Required || !sourceItem.NeedsPublication || sourceItem.NeedsConversion || sourceItem.Status != "ready_after_publication" {
		t.Fatalf("source reference should be ready after publication: %+v", sourceItem)
	}
	if !strings.HasPrefix(sourceItem.ExpectedPublicURI, "https://<asset-host>/ark-inputs/") || sourceItem.RecommendedFileName != "source_reference.mp4" {
		t.Fatalf("unexpected publication target: %+v", sourceItem)
	}
	screenshotItem := plan.Items[1]
	if len(screenshotItem.TaskIDs) != 2 {
		t.Fatalf("screenshot should be reusable across Ark tasks: %+v", screenshotItem)
	}
}

func TestNewArkAssetPublicationPlanBlocksUnsupportedRequiredAssets(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	arkPlan := model.ArkMediaDryRunPlan{
		SourcePackageID: source.PackageID,
		SourceAssetRequirements: []model.ArkMediaSourceAssetRequirement{{
			Ref:               model.DirectorMaterialRef{ID: "source_reference", Kind: "source_reference_video", URI: "https://assets.example.com/source_reference.webm", MimeType: "video/webm"},
			TaskID:            "seedance_reference_director_preview",
			Usage:             "required_reference_video",
			Required:          true,
			AcceptedMimeTypes: []string{"video/mp4", "video/quicktime"},
			RequiresPublicURI: true,
			Status:            "unsupported_mime_type",
		}},
	}

	plan := NewArkAssetPublicationPlan(&source, model.DirectorMaterialRef{ID: "ark_plan", Kind: "ark_media_dry_run_plan", URI: "https://assets.example.com/ark_plan.json", MimeType: "application/json"}, arkPlan, time.Date(2026, 7, 14, 12, 45, 0, 0, time.UTC))

	if plan.Status != "blocked" || len(plan.Blockers) != 1 || plan.Blockers[0].Code != "needs_conversion" {
		t.Fatalf("unsupported required source should block publication: %+v", plan)
	}
	if len(plan.Items) != 1 || !plan.Items[0].NeedsConversion || plan.Items[0].NeedsPublication {
		t.Fatalf("unsupported public source should need conversion, not publication: %+v", plan.Items)
	}
}

func TestNewArkAssetPublicationPlanReportsReadyForPublicHTTPSAssets(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	arkPlan := model.ArkMediaDryRunPlan{
		SourcePackageID: source.PackageID,
		SourceAssetRequirements: []model.ArkMediaSourceAssetRequirement{{
			Ref:               model.DirectorMaterialRef{ID: "source_reference", Kind: "source_reference_video", URI: "https://assets.example.com/source_reference.mp4", MimeType: "video/mp4"},
			TaskID:            "seedance_reference_director_preview",
			Usage:             "required_reference_video",
			Required:          true,
			AcceptedMimeTypes: []string{"video/mp4", "video/quicktime"},
			RequiresPublicURI: true,
			Status:            "ready",
		}},
	}

	plan := NewArkAssetPublicationPlan(&source, model.DirectorMaterialRef{ID: "ark_plan", Kind: "ark_media_dry_run_plan", URI: "https://assets.example.com/ark_plan.json", MimeType: "application/json"}, arkPlan, time.Date(2026, 7, 14, 13, 0, 0, 0, time.UTC))

	if plan.Status != "ready" || len(plan.Blockers) != 0 || len(plan.Warnings) != 0 {
		t.Fatalf("public supported assets should be ready: %+v", plan)
	}
	if len(plan.Items) != 1 || !plan.Items[0].CurrentURIIsPublic || plan.Items[0].ExpectedPublicURI != "https://assets.example.com/source_reference.mp4" {
		t.Fatalf("public source URI should be preserved: %+v", plan.Items)
	}
}

func TestNewArkMediaGenerationResultRegistersProviderURLsAsCandidateArtifacts(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	suggestion := model.DirectorEditSuggestion{
		SuggestionID:    "suggestion_1",
		SourcePackageID: source.PackageID,
		DirectorInputID: "director_input_1",
		ProviderCall: &model.DirectorProviderCall{
			Operation:        "seedance_reference_director_preview",
			Status:           "submitted",
			Provider:         "seedance",
			Model:            "doubao-seedance-2-0-260128",
			TaskID:           "task_1",
			ProviderStatus:   "succeeded",
			RealCallMade:     true,
			NonAuthoritative: true,
			Output: map[string]any{
				"video_url":      "https://assets.example.com/generated/demo.mp4",
				"last_frame_url": "https://assets.example.com/generated/last-frame.png",
				"images": []any{
					map[string]any{"url": "https://assets.example.com/generated/title.webp"},
				},
			},
		},
	}

	result := NewArkMediaGenerationResult(&source, suggestion, time.Date(2026, 7, 15, 19, 30, 0, 0, time.UTC))

	if result.SchemaVersion != model.ArkMediaGenerationResultSchemaVersion || result.Status != "candidate_urls_recorded" || !result.NonAuthoritative {
		t.Fatalf("unexpected generation result identity: %+v", result)
	}
	if len(result.CandidateArtifacts) != 3 {
		t.Fatalf("expected three candidate artifacts, got %+v", result.CandidateArtifacts)
	}
	video := findArtifactByURI(result.CandidateArtifacts, "https://assets.example.com/generated/demo.mp4")
	if video == nil || video.Kind != "generated_video_candidate" || video.MimeType != "video/mp4" || video.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected video candidate artifact: %+v", video)
	}
	if video.Metadata["source_material_policy"] != "non_authoritative_generated_candidate" || video.Metadata["task_id"] != "task_1" {
		t.Fatalf("candidate artifact must preserve provider metadata and boundary: %+v", video.Metadata)
	}
	lastFrame := findArtifactByURI(result.CandidateArtifacts, "https://assets.example.com/generated/last-frame.png")
	if lastFrame == nil || lastFrame.Kind != "generated_image_candidate" || lastFrame.Metadata["asset_role"] != "last_frame_candidate" {
		t.Fatalf("expected last-frame image candidate: %+v", lastFrame)
	}
}

func TestNewArkMediaGenerationResultRecordsPendingTaskWithoutCandidates(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	suggestion := model.DirectorEditSuggestion{
		SuggestionID:    "suggestion_1",
		SourcePackageID: source.PackageID,
		ProviderCall: &model.DirectorProviderCall{
			Status:           "submitted",
			Provider:         "seedance",
			Model:            "doubao-seedance-2-0-260128",
			TaskID:           "task_pending",
			ProviderStatus:   "queued",
			RealCallMade:     true,
			NonAuthoritative: true,
		},
	}

	result := NewArkMediaGenerationResult(&source, suggestion, time.Date(2026, 7, 15, 19, 35, 0, 0, time.UTC))

	if result.Status != "pending_provider_output" || len(result.CandidateArtifacts) != 0 || len(result.Warnings) == 0 {
		t.Fatalf("pending provider task should be explicit and non-fatal: %+v", result)
	}
}

func TestNewArkMediaGenerationResultWithOptionsPollsAndDownloadsCandidateArtifacts(t *testing.T) {
	videoBytes := []byte("fake mp4 bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/generated/demo.mp4" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(videoBytes)
	}))
	defer server.Close()

	source := sampleClientExecutionPackageForExecutorTest(t)
	suggestion := model.DirectorEditSuggestion{
		SuggestionID:    "suggestion_1",
		SourcePackageID: source.PackageID,
		DirectorInputID: "director_input_1",
		ProviderCall: &model.DirectorProviderCall{
			Status:           "submitted",
			Provider:         "seedance",
			Model:            "doubao-seedance-2-0-260128",
			TaskID:           "task_download",
			ProviderStatus:   "queued",
			RealCallMade:     true,
			NonAuthoritative: true,
		},
	}
	client := &fakeArkMediaClient{
		getResult: media.ContentGenerationTaskResult{
			Mode:     config.ArkMediaModeReal,
			Provider: config.ModelProviderSeedance,
			Model:    "doubao-seedance-2-0-260128",
			Response: &media.ContentGenerationTaskResponse{
				ID:     "task_download",
				Status: "succeeded",
				Model:  "doubao-seedance-2-0-260128",
				Output: map[string]any{"video_url": server.URL + "/generated/demo.mp4"},
			},
			Trace: media.ArkMediaCallTrace{
				Provider:     config.ModelProviderSeedance,
				Model:        "doubao-seedance-2-0-260128",
				Mode:         config.ArkMediaModeReal,
				Method:       http.MethodGet,
				EndpointHost: "ark.example",
				EndpointPath: "/api/v3/contents/generations/tasks/task_download",
				HTTPStatus:   http.StatusOK,
			},
		},
	}

	result := NewArkMediaGenerationResultWithOptions(t.Context(), &source, suggestion, time.Date(2026, 7, 15, 20, 0, 0, 0, time.UTC), ArkMediaGenerationOptions{
		OutputDir:        t.TempDir(),
		Client:           client,
		PollAttempts:     1,
		Downloader:       httpArkMediaCandidateDownloader{client: server.Client()},
		MaxDownloadBytes: 1024,
		Now: func() time.Time {
			return time.Date(2026, 7, 15, 20, 0, 1, 0, time.UTC)
		},
	})

	if client.getTaskID != "task_download" || client.getCalls != 1 {
		t.Fatalf("expected one provider poll for task_download, got task=%q calls=%d", client.getTaskID, client.getCalls)
	}
	if result.Status != "candidate_artifacts_downloaded" || result.ProviderStatus != "succeeded" {
		t.Fatalf("expected downloaded generation result, got %+v", result)
	}
	if len(result.PollAttempts) != 1 || result.PollAttempts[0].CandidateURLCount != 1 {
		t.Fatalf("expected poll attempt to record candidate URL count: %+v", result.PollAttempts)
	}
	if len(result.CandidateArtifacts) != 1 || len(result.DownloadedArtifacts) != 1 {
		t.Fatalf("expected one remote candidate and one downloaded artifact: %+v", result)
	}
	downloaded := result.DownloadedArtifacts[0]
	data, err := os.ReadFile(downloaded.URI)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(videoBytes) || downloaded.SHA256 == "" || downloaded.SizeBytes != int64(len(videoBytes)) {
		t.Fatalf("downloaded artifact metadata mismatch: %+v data=%q", downloaded, data)
	}
	if downloaded.Metadata["provider_output_url"] != server.URL+"/generated/demo.mp4" || downloaded.Metadata["include_in_demo"] != false {
		t.Fatalf("downloaded artifact must preserve provider URL and remain opt-in: %+v", downloaded.Metadata)
	}

	review := ReviewArkMediaCandidateAssets(&source, &result, time.Date(2026, 7, 15, 20, 0, 2, 0, time.UTC))
	if review.Status != "approved" || len(review.ApprovedArtifacts) != 1 || result.DownloadedArtifacts[0].Metadata["approved_for_demo"] != true {
		t.Fatalf("downloaded video candidate should pass conservative review: review=%+v artifact=%+v", review, result.DownloadedArtifacts[0])
	}
}

func findArtifactByURI(artifacts []model.ArtifactRef, uri string) *model.ArtifactRef {
	for index := range artifacts {
		if artifacts[index].URI == uri {
			return &artifacts[index]
		}
	}
	return nil
}
