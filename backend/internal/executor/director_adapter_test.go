package executor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

func TestDryRunDirectorAdapterProducesSourceTracedSuggestion(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	source.Metadata = map[string]any{
		"user_demo_intent": map[string]any{
			"raw_prompt": "Show approval completion as the main business value.",
			"must_show":  []any{"Approval status reaches approved."},
			"captions":   []any{"Approved in seconds."},
		},
	}
	recording := model.RecordingResultPackage{ResultID: "result_adapter", SourcePackageID: source.PackageID}
	input := NewDirectorInput(&source, &recording, RenderResult{SourceReferenceVideoPath: "artifacts/render/source_reference.mp4"}, time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC))
	arkPlan := NewArkMediaDryRunPlan(&source, model.DirectorMaterialRef{ID: "director_input", Kind: "director_input", URI: "artifacts/render/director_input.json", MimeType: "application/json"}, input, time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC))

	suggestion, validation, err := NewDryRunDirectorAdapter(func() time.Time {
		return time.Date(2026, 7, 15, 10, 1, 0, 0, time.UTC)
	}).Suggest(t.Context(), input, arkPlan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if suggestion.SchemaVersion != model.DirectorEditSuggestionSchemaVersion || suggestion.Adapter.RealCallMade {
		t.Fatalf("unexpected dry-run suggestion identity: %+v", suggestion)
	}
	if suggestion.ProviderGate == nil || suggestion.ProviderGate.Status != "dry_run" || suggestion.ProviderGate.CanAttemptRealCall {
		t.Fatalf("dry-run suggestion should expose a non-callable provider gate: %+v", suggestion.ProviderGate)
	}
	if len(suggestion.RequirementHandling) == 0 || suggestion.RequirementHandling[0].PrioritySource != "user_explicit_metadata" {
		t.Fatalf("manual user requirement should be handled first: %+v", suggestion.RequirementHandling)
	}
	if len(suggestion.CaptionSuggestions) == 0 || suggestion.CaptionSuggestions[0].Text != "Approval status reaches approved." {
		t.Fatalf("caption suggestions should reflect user/client requirements: %+v", suggestion.CaptionSuggestions)
	}
	if !validation.Valid || validation.SchemaVersion != model.DirectorEditSuggestionValidationSchemaVersion {
		t.Fatalf("dry-run suggestion should validate: %+v", validation)
	}
}

func TestDryRunDirectorAdapterPreservesRuntimeAdaptiveAuthority(t *testing.T) {
	input := minimalDirectorInputForAdapterTest()
	input.StorylinePolicy.RequiredStepOrder = []string{"node_verified", "node_adaptive"}
	input.Workflow.Nodes = []model.DirectorWorkflowStep{{
		NodeID:          "node_verified",
		Order:           1,
		Title:           "Invite teammate",
		Action:          "click",
		ExpectedOutcome: "Invitation action is available",
		Required:        true,
		DurationMS:      1400,
		Verification: &model.DirectorInteractionVerification{
			Status:        "verified",
			Source:        "playwright_readonly_scan",
			SelectorScore: 96,
			Authority:     "verified_product_fact",
			Guidance:      "Treat this interaction as customer-side verified product evidence when planning narrative emphasis.",
		},
	}, {
		NodeID:          "node_adaptive",
		Order:           2,
		Title:           "Generate proposal",
		Goal:            "Find an available generation action on the live page.",
		Action:          "click",
		ExpectedOutcome: "Proposal generation completed",
		Required:        false,
		DurationMS:      1600,
		Verification: &model.DirectorInteractionVerification{
			Status:          "runtime_adaptive",
			Source:          "runtime_adaptive_discovery",
			RuntimeAdaptive: true,
			SelectorScore:   42,
			Authority:       "runtime_adaptive_executable_intent",
			Guidance:        "Treat this as executable intent that may resolve at runtime.",
		},
	}}
	input.Materials.Screenshots = []model.DirectorMaterialRef{{
		ID:           "shot_verified",
		Kind:         "screenshot",
		URI:          "artifacts/recording/step-001.png",
		MimeType:     "image/png",
		SourceNodeID: "node_verified",
	}, {
		ID:           "shot_adaptive",
		Kind:         "screenshot",
		URI:          "artifacts/recording/step-002.png",
		MimeType:     "image/png",
		SourceNodeID: "node_adaptive",
	}}

	suggestion, validation, err := NewDryRunDirectorAdapter(func() time.Time {
		return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	}).Suggest(t.Context(), input, minimalArkPlanForAdapterTest(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !validation.Valid || !hasDirectorValidationPolicy(validation, "runtime_adaptive_not_product_proof") {
		t.Fatalf("runtime-adaptive suggestion should validate with explicit policy: %+v", validation)
	}
	if !strings.Contains(suggestion.Summary, "runtime-adaptive") || !textSliceContains(suggestion.Notes, "Runtime-adaptive interaction steps") {
		t.Fatalf("suggestion should call out runtime-adaptive authority: summary=%q notes=%+v", suggestion.Summary, suggestion.Notes)
	}

	verifiedShot := findDirectorShotByStep(suggestion.ShotSuggestions, "node_verified")
	if verifiedShot == nil || verifiedShot.Operation != "emphasize_verified_source" || verifiedShot.SourceTrace[0].Confidence != "high" {
		t.Fatalf("verified step should be emphasized as high-confidence source: %+v", verifiedShot)
	}
	adaptiveShot := findDirectorShotByStep(suggestion.ShotSuggestions, "node_adaptive")
	if adaptiveShot == nil || adaptiveShot.Operation != "capture_runtime_adaptive_intent" || adaptiveShot.SourceTrace[0].Confidence != "low" {
		t.Fatalf("runtime-adaptive step should remain executable intent with low confidence trace: %+v", adaptiveShot)
	}
	if strings.Contains(strings.ToLower(adaptiveShot.Purpose), "verified") || strings.Contains(strings.ToLower(adaptiveShot.Purpose), "completed") {
		t.Fatalf("runtime-adaptive shot must not overclaim verified outcome: %+v", adaptiveShot)
	}
	adaptiveCaption := findDirectorCaptionByAnchor(suggestion.CaptionSuggestions, "node_adaptive")
	if adaptiveCaption == nil || !strings.Contains(adaptiveCaption.Text, "Capture the resolved action") || strings.Contains(strings.ToLower(adaptiveCaption.Text), "completed") {
		t.Fatalf("runtime-adaptive caption should avoid outcome claims: %+v", adaptiveCaption)
	}
}

func TestConfiguredDirectorAdapterDisabledBlocksProviderCalls(t *testing.T) {
	input := minimalDirectorInputForAdapterTest()
	arkPlan := minimalArkPlanForAdapterTest()

	suggestion, validation, err := NewConfiguredDirectorAdapter(DirectorAdapterOptions{
		ArkMediaMode:     config.ArkMediaModeDisabled,
		APIKeyConfigured: true,
		Now: func() time.Time {
			return time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)
		},
	}).Suggest(t.Context(), input, arkPlan, &model.ArkAssetPublicationResult{CanUseForRealCall: true})
	if err != nil {
		t.Fatal(err)
	}
	if suggestion.Mode != "disabled" || suggestion.ProviderGate == nil || suggestion.ProviderGate.Status != "disabled" || suggestion.Adapter.RealCallMade {
		t.Fatalf("disabled mode should be explicit and non-callable: %+v", suggestion)
	}
	if len(suggestion.ProviderGate.Blockers) == 0 || suggestion.ProviderGate.Blockers[0].Code != "ark_media_disabled" {
		t.Fatalf("disabled provider gate should include blocker: %+v", suggestion.ProviderGate)
	}
	if !validation.Valid {
		t.Fatalf("disabled deterministic suggestion should still validate policy boundaries: %+v", validation)
	}
}

func TestConfiguredDirectorAdapterRealModeBlocksWithoutKeyOrPublishedAssets(t *testing.T) {
	input := minimalDirectorInputForAdapterTest()
	arkPlan := minimalArkPlanForAdapterTest()
	arkPlan.RealCallReadiness.CanCallWhenEnabled = false
	arkPlan.RealCallReadiness.Blockers = []model.ArkMediaReadinessFinding{{
		Code:    "needs_public_uri",
		Message: "source reference must be public",
		RefID:   "source_reference",
		TaskID:  "seedance_reference_director_preview",
	}}

	suggestion, validation, err := NewConfiguredDirectorAdapter(DirectorAdapterOptions{
		ArkMediaMode:     config.ArkMediaModeReal,
		APIKeyConfigured: false,
		Now: func() time.Time {
			return time.Date(2026, 7, 15, 11, 5, 0, 0, time.UTC)
		},
	}).Suggest(t.Context(), input, arkPlan, &model.ArkAssetPublicationResult{
		CanUseForRealCall:  false,
		ContainsDryRunRefs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if suggestion.Mode != "real_preflight" || suggestion.ProviderGate == nil || suggestion.ProviderGate.Status != "blocked_before_provider_call" {
		t.Fatalf("real mode should be blocked before provider call: %+v", suggestion)
	}
	if suggestion.ProviderGate.CanAttemptRealCall || suggestion.Adapter.RealCallMade {
		t.Fatalf("blocked real preflight must not call provider: %+v", suggestion.ProviderGate)
	}
	if !hasReadinessCode(suggestion.ProviderGate.Blockers, "api_key_missing") || !hasReadinessCode(suggestion.ProviderGate.Blockers, "publication_result_not_ready") {
		t.Fatalf("expected key and publication blockers, got %+v", suggestion.ProviderGate.Blockers)
	}
	if !validation.Valid {
		t.Fatalf("blocked preflight suggestion should still validate model boundaries: %+v", validation)
	}
}

func TestConfiguredDirectorAdapterRealModeCanBecomeReadyWithoutCallingProvider(t *testing.T) {
	input := minimalDirectorInputForAdapterTest()
	arkPlan := minimalArkPlanForAdapterTest()
	arkPlan.RealCallReadiness.CanCallWhenEnabled = true
	arkPlan.RealCallReadiness.Blockers = nil

	suggestion, validation, err := NewConfiguredDirectorAdapter(DirectorAdapterOptions{
		ArkMediaMode:     config.ArkMediaModeReal,
		APIKeyConfigured: true,
		Now: func() time.Time {
			return time.Date(2026, 7, 15, 11, 10, 0, 0, time.UTC)
		},
	}).Suggest(t.Context(), input, arkPlan, &model.ArkAssetPublicationResult{CanUseForRealCall: true})
	if err != nil {
		t.Fatal(err)
	}
	if suggestion.ProviderGate == nil || suggestion.ProviderGate.Status != "ready_for_real_call" || !suggestion.ProviderGate.CanAttemptRealCall {
		t.Fatalf("real preflight should be ready when key and assets pass: %+v", suggestion.ProviderGate)
	}
	if suggestion.Adapter.RealCallMade || suggestion.ProviderGate.RealCallMade {
		t.Fatalf("preflight readiness must not imply a real provider call: %+v", suggestion)
	}
	if !validation.Valid {
		t.Fatalf("ready preflight suggestion should validate: %+v", validation)
	}
}

func TestConfiguredDirectorAdapterRealModeSubmitsSeedanceTaskWhenClientReady(t *testing.T) {
	input := minimalDirectorInputForAdapterTest()
	arkPlan := minimalArkPlanForAdapterTest()
	arkPlan.RealCallReadiness.CanCallWhenEnabled = true
	client := &fakeArkMediaClient{
		contentResult: media.ContentGenerationTaskResult{
			Mode:     config.ArkMediaModeReal,
			Provider: config.ModelProviderSeedance,
			Model:    "doubao-seedance-2-0-260128",
			Response: &media.ContentGenerationTaskResponse{ID: "task_seedance_1", Status: "queued", Model: "doubao-seedance-2-0-260128"},
			Trace: media.ArkMediaCallTrace{
				Provider:     config.ModelProviderSeedance,
				Model:        "doubao-seedance-2-0-260128",
				Mode:         config.ArkMediaModeReal,
				Method:       "POST",
				EndpointHost: "ark.example",
				EndpointPath: "/api/v3/contents/generations/tasks",
				HTTPStatus:   200,
				LatencyMS:    12,
			},
		},
	}

	suggestion, validation, err := NewConfiguredDirectorAdapter(DirectorAdapterOptions{
		ArkMediaMode:     config.ArkMediaModeReal,
		APIKeyConfigured: true,
		ArkClient:        client,
		Now: func() time.Time {
			return time.Date(2026, 7, 15, 11, 20, 0, 0, time.UTC)
		},
	}).Suggest(t.Context(), input, arkPlan, readyPublicationResultForAdapterTest())
	if err != nil {
		t.Fatal(err)
	}
	if !validation.Valid {
		t.Fatalf("provider-backed suggestion should validate: %+v", validation)
	}
	if suggestion.Mode != "real_provider" || suggestion.Status != "provider_call_submitted" || !suggestion.Adapter.RealCallMade {
		t.Fatalf("expected real provider suggestion: %+v", suggestion)
	}
	if suggestion.ProviderGate == nil || suggestion.ProviderGate.Status != "provider_call_submitted" || !suggestion.ProviderGate.RealCallMade || !suggestion.ProviderGate.CanAttemptRealCall {
		t.Fatalf("expected submitted provider gate: %+v", suggestion.ProviderGate)
	}
	if suggestion.ProviderCall == nil || suggestion.ProviderCall.TaskID != "task_seedance_1" || suggestion.ProviderCall.ProviderStatus != "queued" || !suggestion.ProviderCall.NonAuthoritative {
		t.Fatalf("expected non-authoritative provider call trace: %+v", suggestion.ProviderCall)
	}
	if len(client.contentRequest.Content) < 2 || client.contentRequest.Content[1].VideoURL == nil || client.contentRequest.Content[1].VideoURL.URL != "https://assets.example.com/source_reference.mp4" {
		t.Fatalf("Seedance request must use published source reference URL: %+v", client.contentRequest)
	}
}

func TestConfiguredDirectorAdapterRealModeRecordsProviderFailureWithoutReturningError(t *testing.T) {
	input := minimalDirectorInputForAdapterTest()
	arkPlan := minimalArkPlanForAdapterTest()
	arkPlan.RealCallReadiness.CanCallWhenEnabled = true
	client := &fakeArkMediaClient{
		contentResult: media.ContentGenerationTaskResult{
			Mode:     config.ArkMediaModeReal,
			Provider: config.ModelProviderSeedance,
			Model:    "doubao-seedance-2-0-260128",
			Trace: media.ArkMediaCallTrace{
				Provider:   config.ModelProviderSeedance,
				Model:      "doubao-seedance-2-0-260128",
				Mode:       config.ArkMediaModeReal,
				Method:     "POST",
				ErrorClass: "http_error",
			},
		},
		contentErr: errors.New("temporary provider outage"),
	}

	suggestion, validation, err := NewConfiguredDirectorAdapter(DirectorAdapterOptions{
		ArkMediaMode:     config.ArkMediaModeReal,
		APIKeyConfigured: true,
		ArkClient:        client,
		Now: func() time.Time {
			return time.Date(2026, 7, 15, 11, 25, 0, 0, time.UTC)
		},
	}).Suggest(t.Context(), input, arkPlan, readyPublicationResultForAdapterTest())
	if err != nil {
		t.Fatal(err)
	}
	if !validation.Valid {
		t.Fatalf("provider failure should preserve policy-valid deterministic suggestion: %+v", validation)
	}
	if suggestion.ProviderGate == nil || suggestion.ProviderGate.Status != "provider_call_failed" || !suggestion.ProviderGate.RealCallMade {
		t.Fatalf("expected failed provider gate with call trace: %+v", suggestion.ProviderGate)
	}
	if suggestion.ProviderCall == nil || suggestion.ProviderCall.Status != "failed" || suggestion.ProviderCall.ErrorClass != "http_error" {
		t.Fatalf("expected provider call failure details: %+v", suggestion.ProviderCall)
	}
}

func TestValidateDirectorEditSuggestionRejectsPriorityViolation(t *testing.T) {
	input := model.DirectorInput{
		StorylinePolicy: model.DirectorStorylinePolicy{RequiredStepOrder: []string{"node_start"}},
		UserIntent: model.DirectorUserIntent{
			Requirements: []model.DirectorUserRequirement{{
				ID:       "req_1",
				Kind:     "must_show",
				Text:     "Show approval",
				Required: true,
				Source:   "user_explicit_metadata",
			}},
		},
	}
	suggestion := model.DirectorEditSuggestion{
		SchemaVersion:        model.DirectorEditSuggestionSchemaVersion,
		SuggestionID:         "suggestion_bad",
		SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly,
		ModelRole:            model.DemoEditModelRolePresentationOptimizerOnly,
		DecisionPriority: model.DirectorDecisionPriority{ResolutionOrder: []model.DirectorPriorityLayer{{
			Rank:   1,
			Source: "cloud_model_suggestion",
		}}},
		ShotSuggestions: []model.DirectorShotSuggestion{{
			ID:             "shot_bad",
			SourceStepID:   "node_unknown",
			Operation:      "emphasize_existing_source",
			PrioritySource: "cloud_model_suggestion",
		}},
	}

	report := ValidateDirectorEditSuggestion(input, suggestion, time.Date(2026, 7, 15, 10, 5, 0, 0, time.UTC))

	if report.Valid {
		t.Fatalf("expected priority/step validation to reject suggestion: %+v", report)
	}
	if len(report.Errors) < 2 {
		t.Fatalf("expected multiple validation errors, got %+v", report.Errors)
	}
}

func minimalDirectorInputForAdapterTest() model.DirectorInput {
	return model.DirectorInput{
		SourcePackageID:      "pkg_adapter",
		DirectorInputID:      "director_input_pkg_adapter",
		DecisionPriority:     directorDecisionPriority(),
		SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly,
		ModelRole:            model.DemoEditModelRolePresentationOptimizerOnly,
		StorylinePolicy: model.DirectorStorylinePolicy{
			PreserveStepOrder: true,
			RequiredStepOrder: []string{"node_start"},
		},
		Workflow: model.DirectorWorkflowSummary{
			Nodes: []model.DirectorWorkflowStep{{
				NodeID:          "node_start",
				Order:           1,
				ExpectedOutcome: "Dashboard is visible",
				Required:        true,
				DurationMS:      1500,
			}},
		},
		Materials: model.DirectorMaterialSet{
			SourceReferenceVideo: &model.DirectorMaterialRef{
				ID:       "source_reference",
				Kind:     "source_reference_video",
				URI:      "https://assets.example/source_reference.mp4",
				MimeType: "video/mp4",
			},
		},
	}
}

func minimalArkPlanForAdapterTest() model.ArkMediaDryRunPlan {
	return model.ArkMediaDryRunPlan{
		VideoProvider: "seedance",
		VideoModel:    "doubao-seedance-2-0-260128",
		RealCallReadiness: model.ArkMediaRealCallReadiness{
			Status:             "ready_when_enabled",
			CanCallWhenEnabled: true,
		},
		RequiredBeforeRealCall: []string{"publish source assets"},
	}
}

func readyPublicationResultForAdapterTest() *model.ArkAssetPublicationResult {
	return &model.ArkAssetPublicationResult{
		CanUseForRealCall: true,
		Items: []model.ArkAssetPublicationResultItem{{
			SourceRef: model.DirectorMaterialRef{
				ID:       "source_reference",
				Kind:     "source_reference_video",
				URI:      "artifacts/render/source_reference.mp4",
				MimeType: "video/mp4",
			},
			ProposedPublicRef: &model.DirectorMaterialRef{
				ID:       "source_reference",
				Kind:     "source_reference_video",
				URI:      "https://assets.example.com/source_reference.mp4",
				MimeType: "video/mp4",
			},
			TaskIDs:           []string{"seedance_reference_director_preview"},
			Required:          true,
			Status:            "published",
			Published:         true,
			CanUseForRealCall: true,
		}},
	}
}

func hasReadinessCode(findings []model.ArkMediaReadinessFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func hasDirectorValidationPolicy(report model.DirectorEditSuggestionValidationReport, policy string) bool {
	for _, value := range report.Policies {
		if value == policy {
			return true
		}
	}
	return false
}

func textSliceContains(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func findDirectorShotByStep(shots []model.DirectorShotSuggestion, stepID string) *model.DirectorShotSuggestion {
	for index := range shots {
		if shots[index].SourceStepID == stepID {
			return &shots[index]
		}
	}
	return nil
}

func findDirectorCaptionByAnchor(captions []model.DirectorCaptionSuggestion, stepID string) *model.DirectorCaptionSuggestion {
	for index := range captions {
		if captions[index].AnchorStepID == stepID {
			return &captions[index]
		}
	}
	return nil
}

type fakeArkMediaClient struct {
	contentRequest media.ContentGenerationTaskRequest
	contentResult  media.ContentGenerationTaskResult
	contentErr     error
	getTaskID      string
	getCalls       int
	getResult      media.ContentGenerationTaskResult
	getErr         error
}

func (c *fakeArkMediaClient) CreateContentGenerationTask(ctx context.Context, request media.ContentGenerationTaskRequest) (media.ContentGenerationTaskResult, error) {
	c.contentRequest = request
	return c.contentResult, c.contentErr
}

func (c *fakeArkMediaClient) GetContentGenerationTask(ctx context.Context, taskID string) (media.ContentGenerationTaskResult, error) {
	c.getTaskID = taskID
	c.getCalls++
	return c.getResult, c.getErr
}

func (c *fakeArkMediaClient) GenerateImages(ctx context.Context, request media.ImageGenerationRequest) (media.ImageGenerationResult, error) {
	return media.ImageGenerationResult{}, nil
}
