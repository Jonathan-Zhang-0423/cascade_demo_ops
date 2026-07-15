package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

const (
	defaultArkBaseURL      = "https://ark.cn-beijing.volces.com/api/v3"
	defaultSeedanceModel   = "doubao-seedance-2-0-260128"
	defaultSeedreamModel   = "doubao-seedream-5-0-pro-260628"
	seedanceTaskEndpoint   = "/contents/generations/tasks"
	seedreamImagesEndpoint = "/images/generations"
)

func enrichRenderResultForDirector(ctx context.Context, source *model.ClientExecutionPackage, recording *model.RecordingResultPackage, renderResult *RenderResult, outputDir string, createdAt time.Time) error {
	if source == nil {
		return errors.New("source package is required for director input")
	}
	if recording == nil {
		return errors.New("recording result package is required for director input")
	}
	if renderResult == nil {
		return errors.New("render result is required for director input")
	}
	if strings.TrimSpace(outputDir) == "" {
		outputDir = filepath.Dir(renderResult.VideoPath)
	}
	if strings.TrimSpace(outputDir) == "" || outputDir == "." {
		outputDir = "artifacts/render"
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	directorInput := NewDirectorInput(source, recording, *renderResult, createdAt)
	directorInputPath := filepath.Join(outputDir, "director_input.json")
	if err := writeIndentedJSONFile(directorInputPath, directorInput); err != nil {
		return err
	}
	renderResult.DirectorInput = &directorInput
	renderResult.DirectorInputPath = directorInputPath

	directorRef := fileMaterialRef(artifactID(source.PackageID, "director_input", 1), "director_input", directorInputPath, "application/json")
	arkPlan := NewArkMediaDryRunPlan(source, directorRef, directorInput, createdAt)
	arkPlanPath := filepath.Join(outputDir, "ark_media_dry_run_plan.json")
	if err := writeIndentedJSONFile(arkPlanPath, arkPlan); err != nil {
		return err
	}
	renderResult.ArkMediaDryRunPlan = &arkPlan
	renderResult.ArkMediaDryRunPlanPath = arkPlanPath

	arkPlanRef := fileMaterialRef(artifactID(source.PackageID, "ark_media_dry_run_plan", 1), "ark_media_dry_run_plan", arkPlanPath, "application/json")
	publicationPlan := NewArkAssetPublicationPlan(source, arkPlanRef, arkPlan, createdAt)
	publicationPlanPath := filepath.Join(outputDir, "ark_asset_publication_plan.json")
	if err := writeIndentedJSONFile(publicationPlanPath, publicationPlan); err != nil {
		return err
	}
	renderResult.ArkAssetPublicationPlan = &publicationPlan
	renderResult.ArkAssetPublicationPlanPath = publicationPlanPath

	publicationPlanRef := fileMaterialRef(artifactID(source.PackageID, "ark_asset_publication_plan", 1), "ark_asset_publication_plan", publicationPlanPath, "application/json")
	publisher := arkAssetPublisherFromEnv(func() time.Time { return createdAt })
	publicationResult, err := publisher.PublishArkAssets(ctx, publicationPlan, publicationPlanRef)
	if err != nil {
		return err
	}
	publicationResultPath := filepath.Join(outputDir, "ark_asset_publication_result.json")
	if err := writeIndentedJSONFile(publicationResultPath, publicationResult); err != nil {
		return err
	}
	renderResult.ArkAssetPublicationResult = &publicationResult
	renderResult.ArkAssetPublicationResultPath = publicationResultPath

	directorAdapter := directorAdapterFromEnv(func() time.Time { return createdAt })
	suggestion, suggestionValidation, err := directorAdapter.Suggest(ctx, directorInput, arkPlan, &publicationResult)
	if err != nil {
		return err
	}
	suggestionPath := filepath.Join(outputDir, "director_edit_suggestion.json")
	if err := writeIndentedJSONFile(suggestionPath, suggestion); err != nil {
		return err
	}
	renderResult.DirectorEditSuggestion = &suggestion
	renderResult.DirectorEditSuggestionPath = suggestionPath

	suggestionValidationPath := filepath.Join(outputDir, "director_edit_suggestion_validation.json")
	if err := writeIndentedJSONFile(suggestionValidationPath, suggestionValidation); err != nil {
		return err
	}
	renderResult.DirectorEditValidation = &suggestionValidation
	renderResult.DirectorEditValidationPath = suggestionValidationPath

	directorPatch := NewDirectorEditPlanPatchFromSuggestion(source, renderResult.DemoEditPlan, directorInput, suggestion, createdAt)
	directorPatchPath := filepath.Join(outputDir, "director_edit_plan_patch.json")
	if err := writeIndentedJSONFile(directorPatchPath, directorPatch); err != nil {
		return err
	}
	renderResult.DirectorEditPlanPatch = &directorPatch
	renderResult.DirectorEditPlanPatchPath = directorPatchPath

	directorPatchValidation := ValidateDirectorEditPlanPatch(renderResult.DemoEditPlan, directorInput, directorPatch, createdAt)
	directorPatchValidationPath := filepath.Join(outputDir, "director_edit_plan_patch_validation.json")
	if err := writeIndentedJSONFile(directorPatchValidationPath, directorPatchValidation); err != nil {
		return err
	}
	renderResult.DirectorEditPlanPatchValidation = &directorPatchValidation
	renderResult.DirectorEditPlanPatchValidationPath = directorPatchValidationPath

	if suggestion.ProviderCall != nil {
		generationResult := NewArkMediaGenerationResultWithOptions(ctx, source, suggestion, createdAt, arkMediaGenerationOptionsFromEnv(outputDir, func() time.Time { return createdAt }))
		candidateReview := ReviewArkMediaCandidateAssets(source, &generationResult, createdAt)
		candidateReviewPath := filepath.Join(outputDir, "candidate_asset_review.json")
		if err := writeIndentedJSONFile(candidateReviewPath, candidateReview); err != nil {
			return err
		}
		renderResult.CandidateAssetReview = &candidateReview
		renderResult.CandidateAssetReviewPath = candidateReviewPath

		candidatePatch := NewCandidateAssetEditPlanPatch(source, renderResult.DemoEditPlan, &candidateReview, createdAt)
		candidatePatchPath := filepath.Join(outputDir, "candidate_asset_edit_plan_patch.json")
		if err := writeIndentedJSONFile(candidatePatchPath, candidatePatch); err != nil {
			return err
		}
		renderResult.CandidateAssetEditPatch = &candidatePatch
		renderResult.CandidateAssetEditPatchPath = candidatePatchPath

		generationResultPath := filepath.Join(outputDir, "ark_media_generation_result.json")
		if err := writeIndentedJSONFile(generationResultPath, generationResult); err != nil {
			return err
		}
		renderResult.ArkMediaGenerationResult = &generationResult
		renderResult.ArkMediaGenerationResultPath = generationResultPath
	}
	return nil
}

func NewDirectorInput(source *model.ClientExecutionPackage, recording *model.RecordingResultPackage, renderResult RenderResult, createdAt time.Time) model.DirectorInput {
	materials := directorMaterialsFrom(source, recording, renderResult)
	requiredStepOrder := requiredStepOrder(source)
	userIntent := directorUserIntent(source)
	return model.DirectorInput{
		SchemaVersion:        model.DirectorInputSchemaVersion,
		DirectorInputID:      "director_input_" + safeID(source.PackageID),
		CreatedAt:            createdAt,
		SourcePackageID:      source.PackageID,
		RecordingResultID:    recording.ResultID,
		Project:              directorProjectContext(source),
		UserIntent:           userIntent,
		DecisionPriority:     directorDecisionPriority(),
		SourceAuthority:      model.DemoEditSourceAuthorityCustomerSideAgent,
		ModelRole:            model.DemoEditModelRolePresentationOptimizerOnly,
		SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly,
		StorylinePolicy: model.DirectorStorylinePolicy{
			PrimaryStorylineSource: "user_explicit_requirements_then_client_execution_package.workflow_graph_and_executable_script_bundle",
			PreserveStepOrder:      true,
			RequiredStepOrder:      requiredStepOrder,
		},
		ModelBoundaries: model.DirectorModelBoundaries{
			CanDo: []string{
				"prioritize explicit user demo requirements before script-derived emphasis and model suggestions",
				"optimize narrative pacing, captions, callouts, zoom/pan hints, transitions, color grade, and music direction",
				"select and reorder optional presentation emphasis only when required step order remains intact",
				"reference existing screenshots and recordings as the only product UI source of truth",
			},
			CannotDo: []string{
				"ignore or downgrade explicit user demo requirements unless they conflict with safety or available source material",
				"generate or replace product UI screenshots or product interaction video",
				"create new browser actions, selectors, test data, or workflow steps",
				"change required step order, source artifact IDs, source step IDs, or source time ranges",
				"use model-generated media as proof of product behavior",
			},
		},
		Workflow:        directorWorkflowSummary(source),
		Recording:       directorRecordingSummary(source, recording),
		Materials:       materials,
		EditPlan:        renderResult.DemoEditPlan,
		RequestedOutput: directorRequestedOutput(source),
		OpenQuestions:   directorOpenQuestions(source, renderResult, materials),
	}
}

func directorDecisionPriority() model.DirectorDecisionPriority {
	return model.DirectorDecisionPriority{
		ResolutionOrder: []model.DirectorPriorityLayer{
			{
				Rank:        1,
				Source:      "user_explicit_requirements",
				Authority:   "highest",
				Description: "Manual prompts, must-show, must-not-show, style, captions, target audience, and explicit delivery requirements must be honored first when they can be satisfied with approved source material.",
			},
			{
				Rank:        2,
				Source:      "client_execution_script",
				Authority:   "product_interaction_authority",
				Description: "The customer-side package owns product interaction facts, executable steps, required step order, and capture timing.",
			},
			{
				Rank:        3,
				Source:      "cloud_model_suggestion",
				Authority:   "presentation_optimizer_only",
				Description: "Cloud-side models may suggest narrative rhythm, captions, camera hints, transitions, color, and music direction only after preserving higher-priority requirements.",
			},
		},
		ConflictPolicy:         "Resolve conflicts as UserIntent > ClientScript > ModelSuggestion. If a user requirement cannot be satisfied from captured source material or violates safety, keep the script safe and report the unsatisfied requirement instead of silently dropping it.",
		UserRequirementPolicy:  "Treat explicit user demo requirements as mandatory delivery intent, not as optional model hints.",
		ClientScriptPolicy:     "Preserve required script order and product facts from workflow_graph and executable_script_bundle.",
		ModelSuggestionPolicy:  "Require source traces for all model suggestions and reject suggestions that invent UI, actions, selectors, or replacement product footage.",
		RequiresSourceTracing:  true,
		RequiresSatisfactionQA: true,
	}
}

func directorUserIntent(source *model.ClientExecutionPackage) model.DirectorUserIntent {
	intent := model.DirectorUserIntent{
		Status:          "package_context_only",
		InputMode:       "full_auto_authorized",
		HighestPriority: true,
	}
	if source == nil {
		return intent
	}
	intent.TargetAudience = source.ProjectContextSummary.TargetAudience
	if intent.TargetAudience != "" {
		intent.SourceTraces = append(intent.SourceTraces, model.DirectorSourceTrace{
			Source:     "client_project_context_summary",
			FieldPath:  "project_context_summary.target_audience",
			Confidence: "high",
		})
	}
	if source.WorkflowGraph != nil && source.WorkflowGraph.Assets != nil && source.WorkflowGraph.Assets.TargetDurationSec > 0 {
		intent.TargetDurationSec = source.WorkflowGraph.Assets.TargetDurationSec
		intent.SourceTraces = append(intent.SourceTraces, model.DirectorSourceTrace{
			Source:     "client_workflow_graph_assets",
			FieldPath:  "workflow_graph.assets.target_duration_sec",
			Confidence: "medium",
		})
	}

	manualCount := addManualUserIntentFromMetadata(&intent, source.Metadata)
	normalizedCount := addNormalizedClientIntent(&intent, source)
	if manualCount > 0 {
		intent.Status = "manual_requirements_present"
		if strings.TrimSpace(intent.RawPrompt) != "" {
			intent.InputMode = "manual_prompt"
		} else {
			intent.InputMode = "manual_structured"
		}
	} else if normalizedCount > 0 {
		intent.Status = "client_normalized_requirements_present"
	}
	intent.Requirements = uniqueDirectorRequirements(intent.Requirements)
	intent.SourceTraces = uniqueDirectorSourceTraces(intent.SourceTraces)
	return intent
}

func addManualUserIntentFromMetadata(intent *model.DirectorUserIntent, metadata map[string]any) int {
	if intent == nil || len(metadata) == 0 {
		return 0
	}
	count := 0
	for _, key := range []string{"user_demo_intent", "manual_demo_requirements", "manual_user_requirements", "user_intent"} {
		value, ok := metadata[key]
		if !ok {
			continue
		}
		count += applyManualIntentValue(intent, value, "metadata."+key)
	}
	return count
}

func applyManualIntentValue(intent *model.DirectorUserIntent, value any, path string) int {
	count := 0
	if text := stringFromAny(value); text != "" {
		intent.RawPrompt = firstNonEmptyString(intent.RawPrompt, text)
		appendDirectorRequirement(intent, model.DirectorUserRequirement{
			ID:         "user_prompt_" + safeID(text),
			Kind:       "raw_prompt",
			Text:       text,
			Required:   true,
			Priority:   1000,
			Source:     "user_explicit_metadata",
			SourcePath: path,
		})
		appendDirectorTrace(intent, "user_explicit_metadata", path, "high")
		return 1
	}
	if values, ok := value.([]any); ok {
		for index, item := range values {
			count += applyManualIntentValue(intent, item, path+"["+strconv.Itoa(index)+"]")
		}
		return count
	}
	if values, ok := value.([]string); ok {
		return appendDirectorRequirementsFromStrings(intent, values, "requirement", true, 950, "user_explicit_metadata", path)
	}
	m, ok := mapFromAny(value)
	if !ok {
		return 0
	}
	appendDirectorTrace(intent, "user_explicit_metadata", path, "high")
	if mode := stringFromAny(m["input_mode"]); mode != "" {
		intent.InputMode = mode
	}
	if prompt := firstNonEmptyString(stringFromAny(m["raw_prompt"]), stringFromAny(m["prompt"]), stringFromAny(m["demo_prompt"])); prompt != "" {
		intent.RawPrompt = firstNonEmptyString(intent.RawPrompt, prompt)
		appendDirectorRequirement(intent, model.DirectorUserRequirement{
			ID:         "user_prompt_" + safeID(prompt),
			Kind:       "raw_prompt",
			Text:       prompt,
			Required:   true,
			Priority:   1000,
			Source:     "user_explicit_metadata",
			SourcePath: path + ".raw_prompt",
		})
		count++
	}
	if audience := stringFromAny(m["target_audience"]); audience != "" {
		intent.TargetAudience = audience
		appendDirectorRequirement(intent, model.DirectorUserRequirement{
			ID:         "target_audience_" + safeID(audience),
			Kind:       "target_audience",
			Text:       audience,
			Required:   true,
			Priority:   1000,
			Source:     "user_explicit_metadata",
			SourcePath: path + ".target_audience",
		})
		count++
	}
	if duration := firstPositiveInt(m["target_duration_sec"], m["duration_sec"], m["duration_seconds"]); duration > 0 {
		intent.TargetDurationSec = duration
		appendDirectorRequirement(intent, model.DirectorUserRequirement{
			ID:         "target_duration_" + strconv.Itoa(duration),
			Kind:       "target_duration",
			Text:       strconv.Itoa(duration) + " seconds",
			Required:   true,
			Priority:   1000,
			Source:     "user_explicit_metadata",
			SourcePath: path + ".target_duration_sec",
		})
		count++
	}
	count += appendDirectorRequirementsFromAny(intent, m["must_show"], "must_show", true, 1000, "user_explicit_metadata", path+".must_show")
	count += appendDirectorRequirementsFromAny(intent, m["must_not_show"], "must_not_show", true, 1000, "user_explicit_metadata", path+".must_not_show")
	count += appendDirectorRequirementsFromAny(intent, m["must_avoid"], "must_not_show", true, 1000, "user_explicit_metadata", path+".must_avoid")
	count += appendDirectorRequirementsFromAny(intent, m["forbidden"], "must_not_show", true, 1000, "user_explicit_metadata", path+".forbidden")
	count += appendDirectorRequirementsFromAny(intent, m["style"], "style", true, 950, "user_explicit_metadata", path+".style")
	count += appendDirectorRequirementsFromAny(intent, m["video_style"], "style", true, 950, "user_explicit_metadata", path+".video_style")
	count += appendDirectorRequirementsFromAny(intent, m["captions"], "caption", true, 950, "user_explicit_metadata", path+".captions")
	count += appendDirectorRequirementsFromAny(intent, m["subtitles"], "caption", true, 950, "user_explicit_metadata", path+".subtitles")
	count += appendDirectorRequirementsFromAny(intent, m["voiceover"], "voiceover", true, 900, "user_explicit_metadata", path+".voiceover")
	count += appendDirectorRequirementsFromAny(intent, m["requirements"], "requirement", true, 950, "user_explicit_metadata", path+".requirements")
	return count
}

func addNormalizedClientIntent(intent *model.DirectorUserIntent, source *model.ClientExecutionPackage) int {
	if intent == nil || source == nil {
		return 0
	}
	count := 0
	for _, goal := range source.ProjectContextSummary.Goals {
		if goal.ValueProposition != "" {
			appendDirectorRequirement(intent, model.DirectorUserRequirement{
				ID:         firstNonEmptyString(goal.ID, "goal_"+safeID(goal.ValueProposition)),
				Kind:       "value_proposition",
				Text:       goal.ValueProposition,
				Required:   true,
				Priority:   firstPositiveInt(goal.Priority, 850),
				Source:     "client_project_context_summary",
				SourcePath: "project_context_summary.goals",
			})
			count++
		}
		for _, criterion := range goal.SuccessCriteria {
			appendDirectorRequirement(intent, model.DirectorUserRequirement{
				ID:         firstNonEmptyString(goal.ID, "success_"+safeID(criterion)),
				Kind:       "success_criteria",
				Text:       criterion,
				Required:   true,
				Priority:   firstPositiveInt(goal.Priority, 850),
				Source:     "client_project_context_summary",
				SourcePath: "project_context_summary.goals.success_criteria",
			})
			count++
		}
	}
	if source.WorkflowGraph != nil && source.WorkflowGraph.Intent != nil {
		graphIntent := source.WorkflowGraph.Intent
		count += appendDirectorRequirementsFromStrings(intent, []string{graphIntent.Objective}, "objective", true, 850, "client_workflow_graph_intent", "workflow_graph.intent.objective")
		count += appendDirectorRequirementsFromStrings(intent, []string{graphIntent.ValueProposition}, "value_proposition", true, 850, "client_workflow_graph_intent", "workflow_graph.intent.value_proposition")
		count += appendDirectorRequirementsFromStrings(intent, graphIntent.SuccessCriteria, "success_criteria", true, 850, "client_workflow_graph_intent", "workflow_graph.intent.success_criteria")
		count += appendDirectorRequirementsFromStrings(intent, []string{graphIntent.DesiredEmotion}, "style", false, 700, "client_workflow_graph_intent", "workflow_graph.intent.desired_emotion")
		count += appendDirectorRequirementsFromStrings(intent, []string{graphIntent.CTA}, "cta", false, 700, "client_workflow_graph_intent", "workflow_graph.intent.cta")
	}
	if source.WorkflowGraph != nil {
		for _, requirement := range source.WorkflowGraph.Requirements {
			if strings.TrimSpace(requirement.Description) == "" {
				continue
			}
			priority := 820
			if !requirement.Required {
				priority = 650
			}
			appendDirectorRequirement(intent, model.DirectorUserRequirement{
				ID:         firstNonEmptyString(requirement.ID, "graph_req_"+safeID(requirement.Description)),
				Kind:       firstNonEmptyString(requirement.Kind, "requirement"),
				Text:       requirement.Description,
				Required:   requirement.Required,
				Priority:   priority,
				Source:     "client_workflow_graph_requirement",
				SourcePath: "workflow_graph.requirements",
				NodeRefs:   append([]string{}, requirement.NodeRefs...),
			})
			count++
		}
	}
	return count
}

func appendDirectorRequirementsFromAny(intent *model.DirectorUserIntent, value any, kind string, required bool, priority int, source string, path string) int {
	if intent == nil || value == nil {
		return 0
	}
	if values, ok := value.([]any); ok {
		count := 0
		for index, item := range values {
			count += appendDirectorRequirementsFromAny(intent, item, kind, required, priority, source, path+"["+strconv.Itoa(index)+"]")
		}
		return count
	}
	if values, ok := value.([]string); ok {
		return appendDirectorRequirementsFromStrings(intent, values, kind, required, priority, source, path)
	}
	if m, ok := mapFromAny(value); ok {
		text := firstNonEmptyString(stringFromAny(m["text"]), stringFromAny(m["description"]), stringFromAny(m["value"]))
		if text == "" {
			return 0
		}
		reqKind := firstNonEmptyString(stringFromAny(m["kind"]), kind)
		reqPriority := firstPositiveInt(m["priority"], priority)
		reqRequired := boolFromAny(m["required"], required)
		id := firstNonEmptyString(stringFromAny(m["id"]), reqKind+"_"+safeID(text))
		appendDirectorRequirement(intent, model.DirectorUserRequirement{
			ID:         id,
			Kind:       reqKind,
			Text:       text,
			Required:   reqRequired,
			Priority:   reqPriority,
			Source:     source,
			SourcePath: path,
			NodeRefs:   stringSliceFromAny(m["node_refs"]),
		})
		return 1
	}
	if text := stringFromAny(value); text != "" {
		return appendDirectorRequirementsFromStrings(intent, []string{text}, kind, required, priority, source, path)
	}
	return 0
}

func appendDirectorRequirementsFromStrings(intent *model.DirectorUserIntent, values []string, kind string, required bool, priority int, source string, path string) int {
	count := 0
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		appendDirectorRequirement(intent, model.DirectorUserRequirement{
			ID:         kind + "_" + safeID(value),
			Kind:       kind,
			Text:       value,
			Required:   required,
			Priority:   priority,
			Source:     source,
			SourcePath: path,
		})
		count++
	}
	return count
}

func appendDirectorRequirement(intent *model.DirectorUserIntent, requirement model.DirectorUserRequirement) {
	requirement.Text = strings.TrimSpace(requirement.Text)
	if intent == nil || requirement.Text == "" {
		return
	}
	requirement.Kind = firstNonEmptyString(requirement.Kind, "requirement")
	requirement.Source = firstNonEmptyString(requirement.Source, "unknown")
	if requirement.ID == "" {
		requirement.ID = requirement.Kind + "_" + safeID(requirement.Text)
	}
	if requirement.Priority == 0 {
		requirement.Priority = 500
	}
	intent.Requirements = append(intent.Requirements, requirement)
}

func appendDirectorTrace(intent *model.DirectorUserIntent, source string, fieldPath string, confidence string) {
	if intent == nil || source == "" || fieldPath == "" {
		return
	}
	intent.SourceTraces = append(intent.SourceTraces, model.DirectorSourceTrace{
		Source:     source,
		FieldPath:  fieldPath,
		Confidence: firstNonEmptyString(confidence, "medium"),
	})
}

func uniqueDirectorRequirements(values []model.DirectorUserRequirement) []model.DirectorUserRequirement {
	out := []model.DirectorUserRequirement{}
	seen := map[string]bool{}
	for _, value := range values {
		key := value.Source + ":" + value.Kind + ":" + strings.ToLower(strings.TrimSpace(value.Text))
		if value.Text == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func uniqueDirectorSourceTraces(values []model.DirectorSourceTrace) []model.DirectorSourceTrace {
	out := []model.DirectorSourceTrace{}
	seen := map[string]bool{}
	for _, value := range values {
		key := value.Source + ":" + value.FieldPath
		if value.Source == "" || value.FieldPath == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func NewArkMediaDryRunPlan(source *model.ClientExecutionPackage, directorRef model.DirectorMaterialRef, input model.DirectorInput, createdAt time.Time) model.ArkMediaDryRunPlan {
	videoBaseURL := envOrDefaultTrimmed("SEEDANCE_BASE_URL", defaultArkBaseURL)
	videoModel := envOrDefaultTrimmed("SEEDANCE_MODEL", defaultSeedanceModel)
	imageBaseURL := envOrDefaultTrimmed("SEEDREAM_BASE_URL", envOrDefaultTrimmed("DOUBAO_BASE_URL", defaultArkBaseURL))
	imageModel := envOrDefaultTrimmed("SEEDREAM_MODEL", defaultSeedreamModel)
	seedanceRefs := seedanceInputRefs(input.Materials)
	seedreamRefs := screenshotInputRefs(input.Materials)

	tasks := []model.ArkMediaTaskSpec{
		{
			TaskID:   "seedance_reference_director_preview",
			Kind:     "video_generation_dry_run",
			Endpoint: joinEndpoint(videoBaseURL, seedanceTaskEndpoint),
			Method:   "POST",
			Model:    videoModel,
			Purpose:  "Optional future Seedance pass for non-authoritative director preview based on existing captured source material.",
			RequestBody: map[string]any{
				"model":             videoModel,
				"content":           seedanceContentPreview(input),
				"resolution":        "1080p",
				"ratio":             "16:9",
				"duration":          5,
				"generate_audio":    false,
				"return_last_frame": true,
				"watermark":         false,
			},
			InputRefs: seedanceRefs,
			OutputPolicy: model.ArkMediaOutputPolicy{
				DownloadImmediately: true,
				ExpectedFormats:     []string{"mp4", "png"},
				URLTTLHours:         24,
				StoreAsArtifact:     true,
			},
		},
		{
			TaskID:   "seedream_non_product_visuals",
			Kind:     "image_generation_dry_run",
			Endpoint: joinEndpoint(imageBaseURL, seedreamImagesEndpoint),
			Method:   "POST",
			Model:    imageModel,
			Purpose:  "Optional future Seedream pass for title cards, abstract brand visuals, or section dividers only; never product UI.",
			RequestBody: map[string]any{
				"model":           imageModel,
				"prompt":          seedreamPromptPreview(input),
				"size":            "2K",
				"response_format": "url",
				"output_format":   "png",
				"watermark":       false,
			},
			InputRefs: seedreamRefs,
			OutputPolicy: model.ArkMediaOutputPolicy{
				DownloadImmediately: true,
				ExpectedFormats:     []string{"png", "jpeg"},
				URLTTLHours:         24,
				StoreAsArtifact:     true,
			},
		},
	}
	constraints := arkMediaConstraints(videoModel, imageModel)
	requirements := arkMediaSourceRequirements(seedanceRefs, seedreamRefs)

	return model.ArkMediaDryRunPlan{
		SchemaVersion:           model.ArkMediaDryRunPlanSchemaVersion,
		PlanID:                  "ark_media_dry_run_" + safeID(source.PackageID),
		CreatedAt:               createdAt,
		Mode:                    "dry_run",
		Reason:                  "Real Ark media calls are disabled until DirectorInput, public asset upload/download, cost controls, and source-only policy checks are explicitly enabled.",
		SourcePackageID:         source.PackageID,
		DirectorInputRef:        directorRef,
		VideoProvider:           "seedance",
		VideoBaseURL:            videoBaseURL,
		VideoModel:              videoModel,
		ImageProvider:           "seedream",
		ImageBaseURL:            imageBaseURL,
		ImageModel:              imageModel,
		RecommendedTasks:        tasks,
		ProviderConstraints:     constraints,
		SourceAssetRequirements: requirements,
		OutputHandling:          arkMediaOutputHandling(),
		RealCallReadiness:       arkMediaRealCallReadiness(source, requirements),
		RequiredBeforeRealCall: []string{
			"publish selected source_reference_video or screenshots as time-limited URLs accessible by Ark, or encode small references as Base64 within provider limits",
			"trim Seedance reference videos to 2-15 seconds each and no more than 15 seconds total",
			"download returned video_url, last_frame_url, or image URL artifacts within 24 hours",
			"validate generated media is non-authoritative and cannot replace captured product UI evidence",
			"record provider request/response metadata without persisting raw API keys",
		},
	}
}

func NewArkAssetPublicationPlan(source *model.ClientExecutionPackage, arkPlanRef model.DirectorMaterialRef, arkPlan model.ArkMediaDryRunPlan, createdAt time.Time) model.ArkAssetPublicationPlan {
	items := arkAssetPublicationItems(arkPlan.SourceAssetRequirements)
	blockers := []model.ArkMediaReadinessFinding{}
	warnings := []model.ArkMediaReadinessFinding{}
	for _, item := range items {
		if item.Status == "ready" {
			continue
		}
		finding := model.ArkMediaReadinessFinding{
			Code:    item.Status,
			Message: firstNonEmptyString(item.ActionRequired, "source asset is not ready for Ark media publication"),
			RefID:   item.Ref.ID,
			TaskID:  strings.Join(item.TaskIDs, ","),
		}
		if item.Required && item.Status != "ready_after_publication" {
			blockers = append(blockers, finding)
			continue
		}
		warnings = append(warnings, finding)
	}
	status := "ready"
	if len(blockers) > 0 {
		status = "blocked"
	} else {
		for _, item := range items {
			if item.NeedsPublication {
				status = "needs_publication"
				break
			}
		}
	}
	sourcePackageID := arkPlan.SourcePackageID
	if source != nil && source.PackageID != "" {
		sourcePackageID = source.PackageID
	}
	return model.ArkAssetPublicationPlan{
		SchemaVersion:       model.ArkAssetPublicationPlanSchemaVersion,
		PlanID:              "ark_asset_publication_" + safeID(sourcePackageID),
		CreatedAt:           createdAt,
		Mode:                "dry_run",
		SourcePackageID:     sourcePackageID,
		ArkMediaDryRunRef:   arkPlanRef,
		Status:              status,
		PublicationStrategy: "publish selected local captured assets as short-lived HTTPS URLs or provider asset references before CASCADE_ARK_MEDIA_MODE=real",
		URLTTLHours:         24,
		Items:               items,
		Blockers:            blockers,
		Warnings:            warnings,
		Notes: []string{
			"Dry-run plan only; no bytes are uploaded and no provider calls are made.",
			"Published assets must remain captured source material; generated media cannot replace product UI evidence.",
		},
	}
}

func NewArkMediaGenerationResult(source *model.ClientExecutionPackage, suggestion model.DirectorEditSuggestion, createdAt time.Time) model.ArkMediaGenerationResult {
	sourcePackageID := suggestion.SourcePackageID
	if source != nil && source.PackageID != "" {
		sourcePackageID = source.PackageID
	}
	call := suggestion.ProviderCall
	candidates := providerCandidateArtifacts(sourcePackageID, call, createdAt)
	status := "no_provider_call"
	provider := ""
	modelName := ""
	taskID := ""
	providerStatus := ""
	if call != nil {
		provider = call.Provider
		modelName = call.Model
		taskID = call.TaskID
		providerStatus = call.ProviderStatus
		status = generationResultStatus(*call, len(candidates))
	}
	warnings := []model.ArkMediaReadinessFinding{}
	if call != nil && call.RealCallMade && len(candidates) == 0 && call.Status != "failed" {
		warnings = append(warnings, model.ArkMediaReadinessFinding{
			Code:    "no_candidate_urls",
			Message: "provider response did not include downloadable video or image URLs yet; poll task result before final media ingestion",
			TaskID:  taskID,
		})
	}
	return model.ArkMediaGenerationResult{
		SchemaVersion:      model.ArkMediaGenerationResultSchemaVersion,
		ResultID:           "ark_media_generation_result_" + safeID(firstNonEmptyString(sourcePackageID, suggestion.SuggestionID)),
		CreatedAt:          createdAt,
		SourcePackageID:    sourcePackageID,
		DirectorInputID:    suggestion.DirectorInputID,
		SuggestionID:       suggestion.SuggestionID,
		Status:             status,
		Provider:           provider,
		Model:              modelName,
		TaskID:             taskID,
		ProviderStatus:     providerStatus,
		NonAuthoritative:   true,
		CandidateArtifacts: candidates,
		ProviderCall:       call,
		Warnings:           warnings,
		Notes: []string{
			"Generated provider URLs are registered as non-authoritative candidate artifacts only.",
			"Candidate artifacts default to include_in_demo=false and must not replace captured product UI evidence.",
			"Download provider URLs within their TTL before using them in final composition.",
		},
	}
}

func generationResultStatus(call model.DirectorProviderCall, candidateCount int) string {
	if call.Status == "failed" || call.ErrorClass != "" {
		return "provider_call_failed"
	}
	if candidateCount > 0 {
		return "candidate_urls_recorded"
	}
	switch strings.ToLower(strings.TrimSpace(call.ProviderStatus)) {
	case "succeeded", "success", "completed", "done":
		return "completed_without_candidate_urls"
	case "queued", "running", "pending", "processing", "":
		return "pending_provider_output"
	default:
		return "provider_output_pending_or_unknown"
	}
}

func providerCandidateArtifacts(sourcePackageID string, call *model.DirectorProviderCall, createdAt time.Time) []model.ArtifactRef {
	if call == nil || len(call.Output) == 0 {
		return nil
	}
	candidates := extractProviderOutputURLCandidates(call.Output)
	artifacts := make([]model.ArtifactRef, 0, len(candidates))
	for index, candidate := range candidates {
		metadata := map[string]any{
			"asset_role":             candidate.Role,
			"include_in_demo":        false,
			"source_material_policy": "non_authoritative_generated_candidate",
			"provider":               call.Provider,
			"model":                  call.Model,
			"task_id":                call.TaskID,
			"provider_status":        call.ProviderStatus,
			"provider_output_path":   candidate.OutputPath,
			"non_authoritative":      true,
		}
		artifacts = append(artifacts, model.ArtifactRef{
			ID:        artifactID(sourcePackageID, candidate.Kind, index+1),
			Kind:      candidate.Kind,
			URI:       candidate.URI,
			MimeType:  candidate.MimeType,
			SHA256:    model.SHA256Hex([]byte(candidate.URI)),
			CreatedAt: createdAt,
			Sensitive: false,
			Metadata:  metadata,
		})
	}
	return artifacts
}

type providerOutputURLCandidate struct {
	URI        string
	Kind       string
	Role       string
	MimeType   string
	OutputPath string
}

func extractProviderOutputURLCandidates(output map[string]any) []providerOutputURLCandidate {
	candidates := []providerOutputURLCandidate{}
	seen := map[string]bool{}
	var visit func(value any, path string)
	visit = func(value any, path string) {
		switch typed := value.(type) {
		case string:
			if !isHTTPURL(typed) || seen[typed] {
				return
			}
			candidate, ok := inferProviderOutputCandidate(typed, path)
			if !ok {
				return
			}
			seen[typed] = true
			candidates = append(candidates, candidate)
		case []any:
			for index, item := range typed {
				visit(item, path+"["+strconv.Itoa(index)+"]")
			}
		case map[string]any:
			for key, item := range typed {
				nextPath := key
				if path != "" {
					nextPath = path + "." + key
				}
				visit(item, nextPath)
			}
		case map[string]string:
			for key, item := range typed {
				nextPath := key
				if path != "" {
					nextPath = path + "." + key
				}
				visit(item, nextPath)
			}
		}
	}
	visit(output, "output")
	return candidates
}

func inferProviderOutputCandidate(rawURL string, outputPath string) (providerOutputURLCandidate, bool) {
	lowerPath := strings.ToLower(outputPath)
	lowerURL := strings.ToLower(rawURL)
	ext := providerURLExtension(lowerURL)
	candidate := providerOutputURLCandidate{
		URI:        rawURL,
		Kind:       "generated_media_candidate",
		Role:       "provider_generated_candidate",
		MimeType:   "application/octet-stream",
		OutputPath: outputPath,
	}
	switch {
	case strings.Contains(lowerPath, "last_frame"):
		candidate.Kind = "generated_image_candidate"
		candidate.Role = "last_frame_candidate"
		candidate.MimeType = imageMimeForExtension(ext)
	case strings.Contains(lowerPath, "image") || ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp":
		candidate.Kind = "generated_image_candidate"
		candidate.Role = "generated_image_candidate"
		candidate.MimeType = imageMimeForExtension(ext)
	case strings.Contains(lowerPath, "video") || ext == ".mp4" || ext == ".mov" || ext == ".webm":
		candidate.Kind = "generated_video_candidate"
		candidate.Role = "director_preview_candidate"
		candidate.MimeType = videoMimeForExtension(ext)
	case !strings.Contains(lowerPath, "url"):
		return providerOutputURLCandidate{}, false
	}
	return candidate, true
}

func providerURLExtension(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return strings.ToLower(filepath.Ext(rawURL))
	}
	return strings.ToLower(filepath.Ext(parsed.Path))
}

func imageMimeForExtension(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

func videoMimeForExtension(ext string) string {
	switch ext {
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	default:
		return "video/mp4"
	}
}

func arkAssetPublicationItems(requirements []model.ArkMediaSourceAssetRequirement) []model.ArkAssetPublicationItem {
	byKey := map[string]int{}
	items := []model.ArkAssetPublicationItem{}
	for _, requirement := range requirements {
		key := requirement.Ref.ID
		if key == "" {
			key = requirement.Ref.URI
		}
		if key == "" {
			key = requirement.TaskID + ":" + requirement.Usage
		}
		if index, ok := byKey[key]; ok {
			items[index].TaskIDs = compactStrings(append(items[index].TaskIDs, requirement.TaskID))
			items[index].Required = items[index].Required || requirement.Required
			items[index].AcceptedMimeTypes = mergeStrings(items[index].AcceptedMimeTypes, requirement.AcceptedMimeTypes)
			if items[index].Usage != requirement.Usage {
				items[index].Usage = compactStrings([]string{items[index].Usage, requirement.Usage})[0]
			}
			continue
		}
		item := arkAssetPublicationItem(requirement)
		byKey[key] = len(items)
		items = append(items, item)
	}
	return items
}

func arkAssetPublicationItem(requirement model.ArkMediaSourceAssetRequirement) model.ArkAssetPublicationItem {
	public := isPublicHTTPSURL(requirement.Ref.URI)
	mimeOK := mimeTypeAllowed(requirement.Ref.MimeType, requirement.AcceptedMimeTypes)
	status := "ready"
	action := ""
	needsPublication := false
	needsConversion := false
	switch {
	case requirement.Ref.ID == "" && requirement.Ref.URI == "":
		status = "missing"
		action = "produce this captured source asset before enabling real Ark media calls"
	case !mimeOK:
		status = "needs_conversion"
		needsConversion = true
		action = "convert this source asset to one of the accepted formats before publication"
	case !public:
		status = "ready_after_publication"
		needsPublication = true
		action = "publish this captured source asset as a short-lived HTTPS URL or provider asset reference"
	}
	return model.ArkAssetPublicationItem{
		Ref:                 requirement.Ref,
		TaskIDs:             compactStrings([]string{requirement.TaskID}),
		Usage:               requirement.Usage,
		Required:            requirement.Required,
		AcceptedMimeTypes:   append([]string{}, requirement.AcceptedMimeTypes...),
		CurrentURIIsPublic:  public,
		NeedsPublication:    needsPublication,
		NeedsConversion:     needsConversion,
		RecommendedFileName: arkAssetPublicationFileName(requirement.Ref),
		ExpectedPublicURI:   arkAssetExpectedPublicURI(requirement.Ref),
		Status:              status,
		ActionRequired:      action,
	}
}

func arkAssetPublicationFileName(ref model.DirectorMaterialRef) string {
	base := safeID(firstNonEmptyString(ref.ID, ref.Kind, "asset"))
	ext := extensionForMimeType(ref.MimeType)
	if ext == "" {
		ext = strings.ToLower(filepath.Ext(ref.URI))
	}
	if ext == "" {
		return base
	}
	return base + ext
}

func arkAssetExpectedPublicURI(ref model.DirectorMaterialRef) string {
	if isPublicHTTPSURL(ref.URI) {
		return ref.URI
	}
	return "https://<asset-host>/ark-inputs/" + arkAssetPublicationFileName(ref)
}

func extensionForMimeType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "application/json":
		return ".json"
	default:
		return ""
	}
}

func arkMediaConstraints(videoModel string, imageModel string) model.ArkMediaConstraints {
	return model.ArkMediaConstraints{
		Seedance: model.ArkMediaVideoConstraints{
			Model:                      videoModel,
			MinDurationSec:             4,
			MaxDurationSec:             15,
			MaxReferenceVideoTotalSec:  15,
			MaxReferenceInputCount:     4,
			AcceptedVideoMimeTypes:     []string{"video/mp4", "video/quicktime"},
			AcceptedImageMimeTypes:     []string{"image/png", "image/jpeg"},
			RequiresPublicHTTPAssets:   true,
			AllowsAudioGeneration:      false,
			NonAuthoritativeOutputOnly: true,
		},
		Seedream: model.ArkMediaImageConstraints{
			Model:                      imageModel,
			AcceptedOutputFormats:      []string{"png", "jpeg"},
			RequiresPublicHTTPAssets:   true,
			NonProductVisualsOnly:      true,
			NonAuthoritativeOutputOnly: true,
		},
	}
}

func arkMediaSourceRequirements(seedanceRefs []model.DirectorMaterialRef, seedreamRefs []model.DirectorMaterialRef) []model.ArkMediaSourceAssetRequirement {
	requirements := []model.ArkMediaSourceAssetRequirement{}
	hasSeedanceVideo := false
	for _, ref := range seedanceRefs {
		required := strings.HasPrefix(ref.MimeType, "video/")
		if required {
			hasSeedanceVideo = true
		}
		requirements = append(requirements, arkMediaSourceRequirement("seedance_reference_director_preview", "reference_video_or_image", ref, required))
	}
	if !hasSeedanceVideo {
		requirements = append(requirements, model.ArkMediaSourceAssetRequirement{
			Ref:               model.DirectorMaterialRef{Kind: "source_reference_video", MimeType: "video/mp4"},
			TaskID:            "seedance_reference_director_preview",
			Usage:             "required_reference_video",
			Required:          true,
			AcceptedMimeTypes: []string{"video/mp4", "video/quicktime"},
			RequiresPublicURI: true,
			Status:            "missing",
			ActionRequired:    "produce source_reference.mp4 from the captured recording before enabling real Seedance calls",
		})
	}
	for _, ref := range seedreamRefs {
		requirements = append(requirements, arkMediaSourceRequirement("seedream_non_product_visuals", "optional_style_reference_image", ref, false))
	}
	return requirements
}

func arkMediaSourceRequirement(taskID string, usage string, ref model.DirectorMaterialRef, required bool) model.ArkMediaSourceAssetRequirement {
	accepted := acceptedArkMediaMimeTypes(ref)
	public := isPublicHTTPSURL(ref.URI)
	status := "ready"
	action := ""
	switch {
	case ref.ID == "" && ref.URI == "":
		status = "missing"
		action = "provide a captured source asset before enabling this model task"
	case !mimeTypeAllowed(ref.MimeType, accepted):
		status = "unsupported_mime_type"
		action = "convert or replace the source asset with an accepted format"
	case !public:
		status = "needs_public_uri"
		action = "publish this asset as a time-limited HTTPS URL or provider asset reference before real Ark calls"
	}
	return model.ArkMediaSourceAssetRequirement{
		Ref:                ref,
		TaskID:             taskID,
		Usage:              usage,
		Required:           required,
		AcceptedMimeTypes:  accepted,
		RequiresPublicURI:  true,
		CurrentURIIsPublic: public,
		Status:             status,
		ActionRequired:     action,
	}
}

func acceptedArkMediaMimeTypes(ref model.DirectorMaterialRef) []string {
	switch {
	case strings.HasPrefix(ref.MimeType, "video/"):
		return []string{"video/mp4", "video/quicktime"}
	case strings.HasPrefix(ref.MimeType, "image/"):
		return []string{"image/png", "image/jpeg"}
	default:
		return []string{"video/mp4", "video/quicktime", "image/png", "image/jpeg"}
	}
}

func mimeTypeAllowed(value string, allowed []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, candidate := range allowed {
		if value == strings.ToLower(candidate) {
			return true
		}
	}
	return false
}

func arkMediaOutputHandling() model.ArkMediaOutputHandling {
	return model.ArkMediaOutputHandling{
		DownloadWithinHours:            24,
		StoreAsArtifact:                true,
		RecordRedactedProviderMetadata: true,
		NonAuthoritativeOutputOnly:     true,
		MustNotReplaceCapturedUI:       true,
		ExpectedOutputs: []model.ArkMediaExpectedOutput{
			{
				TaskID:        "seedance_reference_director_preview",
				Kind:          "generated_video_candidate",
				Role:          "director_preview_candidate",
				MimeTypes:     []string{"video/mp4"},
				IncludeInDemo: false,
			},
			{
				TaskID:        "seedance_reference_director_preview",
				Kind:          "generated_image_candidate",
				Role:          "last_frame_candidate",
				MimeTypes:     []string{"image/png", "image/jpeg"},
				IncludeInDemo: false,
			},
			{
				TaskID:        "seedream_non_product_visuals",
				Kind:          "generated_image_candidate",
				Role:          "non_product_visual_candidate",
				MimeTypes:     []string{"image/png", "image/jpeg"},
				IncludeInDemo: false,
			},
		},
	}
}

func arkMediaRealCallReadiness(source *model.ClientExecutionPackage, requirements []model.ArkMediaSourceAssetRequirement) model.ArkMediaRealCallReadiness {
	blockers := []model.ArkMediaReadinessFinding{}
	warnings := []model.ArkMediaReadinessFinding{}
	for _, requirement := range requirements {
		if requirement.Status == "ready" {
			continue
		}
		finding := model.ArkMediaReadinessFinding{
			Code:    requirement.Status,
			Message: firstNonEmptyString(requirement.ActionRequired, "source asset is not ready for real Ark calls"),
			RefID:   requirement.Ref.ID,
			TaskID:  requirement.TaskID,
		}
		if requirement.Required {
			blockers = append(blockers, finding)
			continue
		}
		warnings = append(warnings, finding)
	}
	if source != nil && source.WorkflowGraph != nil && source.WorkflowGraph.Assets != nil && source.WorkflowGraph.Assets.TargetDurationSec > 15 {
		warnings = append(warnings, model.ArkMediaReadinessFinding{
			Code:    "seedance_duration_limit",
			Message: "Seedance 2.0 supports short 4-15 second generations; longer demos must remain composed from captured footage plus optional short candidates.",
			TaskID:  "seedance_reference_director_preview",
		})
	}
	status := "ready_when_enabled"
	if len(blockers) > 0 {
		status = "blocked"
	}
	return model.ArkMediaRealCallReadiness{
		Status:             status,
		CanCallNow:         false,
		CanCallWhenEnabled: len(blockers) == 0,
		ModeGate:           "CASCADE_ARK_MEDIA_MODE must be set to real before any provider request is sent",
		Blockers:           blockers,
		Warnings:           warnings,
	}
}

func directorProjectContext(source *model.ClientExecutionPackage) model.DirectorProjectContext {
	goals := make([]string, 0, len(source.ProjectContextSummary.Goals))
	for _, goal := range source.ProjectContextSummary.Goals {
		if goal.ValueProposition != "" {
			goals = append(goals, goal.ValueProposition)
		}
		goals = append(goals, goal.SuccessCriteria...)
	}
	useCases := make([]string, 0, len(source.ProjectContextSummary.UseCases))
	for _, useCase := range source.ProjectContextSummary.UseCases {
		if useCase != "" {
			useCases = append(useCases, string(useCase))
		}
	}
	return model.DirectorProjectContext{
		OrgID:          source.OrgID,
		ProjectID:      source.ProjectID,
		ProductURL:     firstNonEmptyString(source.ProjectContextSummary.ProductURL, source.RecordingRunSpec.BaseURL),
		ProductName:    source.ProjectContextSummary.Name,
		TargetAudience: source.ProjectContextSummary.TargetAudience,
		Goals:          compactStrings(goals),
		UseCases:       compactStrings(useCases),
	}
}

func directorWorkflowSummary(source *model.ClientExecutionPackage) model.DirectorWorkflowSummary {
	if source.WorkflowGraph == nil {
		return model.DirectorWorkflowSummary{}
	}
	nodes := []model.DirectorWorkflowStep{}
	timingByNode := recordingTimingHintsByNode(source)
	captureByNode := recordingCaptureWindowsByNode(source)
	for index, node := range source.WorkflowGraph.Nodes {
		if node == nil {
			continue
		}
		action := node.Action
		target := model.ActionTarget{Selector: node.Selector}
		if node.ActionSpec != nil {
			action = string(node.ActionSpec.Type)
			target = node.ActionSpec.Target
		}
		nodes = append(nodes, model.DirectorWorkflowStep{
			NodeID:          node.ID,
			Order:           index + 1,
			Action:          action,
			Title:           node.Title,
			Goal:            node.Goal,
			Target:          target,
			ExpectedOutcome: node.ExpectedOutcome,
			Narrative:       cloneNarrativeCue(node.Narrative),
			Capture:         cloneCaptureSpec(node.Capture),
			Timing:          directorStepTiming(node, timingByNode),
			CaptureWindow:   directorStepCaptureWindow(node.ID, captureByNode),
			Verification:    directorStepVerification(node),
			Required:        directorStepRequired(node),
			DurationMS:      directorStepDurationMS(node, timingByNode),
			EvidenceRefs:    append([]model.EvidenceRef{}, node.EvidenceRefs...),
			Tags:            append([]string{}, node.Tags...),
		})
	}
	return model.DirectorWorkflowSummary{
		GraphID:    source.WorkflowGraph.ID,
		Version:    source.WorkflowGraph.Version,
		EntryPoint: source.WorkflowGraph.EntryPoint,
		Intent:     source.WorkflowGraph.Intent,
		Nodes:      nodes,
		Edges:      source.WorkflowGraph.Edges,
		Assets:     source.WorkflowGraph.Assets,
	}
}

func recordingTimingHintsByNode(source *model.ClientExecutionPackage) map[string]model.NodeTimingHint {
	out := map[string]model.NodeTimingHint{}
	if source == nil {
		return out
	}
	for _, hint := range source.RecordingRunSpec.Timeline.NodeTimingHints {
		if strings.TrimSpace(hint.NodeID) == "" {
			continue
		}
		out[hint.NodeID] = hint
	}
	return out
}

func recordingCaptureWindowsByNode(source *model.ClientExecutionPackage) map[string]model.CaptureWindow {
	out := map[string]model.CaptureWindow{}
	if source == nil {
		return out
	}
	for _, window := range source.RecordingRunSpec.Timeline.CaptureWindows {
		if strings.TrimSpace(window.NodeID) == "" {
			continue
		}
		out[window.NodeID] = window
	}
	return out
}

func directorStepTiming(node *model.GraphNode, timingByNode map[string]model.NodeTimingHint) *model.NodeTimingHint {
	if node == nil {
		return nil
	}
	if hint, ok := timingByNode[node.ID]; ok {
		copy := hint
		return &copy
	}
	if node.DurationHintMS <= 0 {
		return nil
	}
	return &model.NodeTimingHint{NodeID: node.ID, DurationMS: node.DurationHintMS}
}

func directorStepCaptureWindow(nodeID string, captureByNode map[string]model.CaptureWindow) *model.CaptureWindow {
	if window, ok := captureByNode[nodeID]; ok {
		copy := window
		return &copy
	}
	return nil
}

func directorStepDurationMS(node *model.GraphNode, timingByNode map[string]model.NodeTimingHint) int {
	if node == nil {
		return 0
	}
	if hint, ok := timingByNode[node.ID]; ok && hint.DurationMS > 0 {
		return hint.DurationMS
	}
	return node.DurationHintMS
}

func directorStepRequired(node *model.GraphNode) bool {
	if node == nil {
		return false
	}
	if directorStepIsRuntimeAdaptive(node) {
		return false
	}
	for _, validation := range node.Validations {
		if validation.Required && strings.EqualFold(validation.Severity, "blocking") {
			return true
		}
	}
	for _, assertion := range node.StateAfter {
		if assertion.Required {
			return true
		}
	}
	return node.Type != model.GraphNodeTypeNarrative && node.Type != model.GraphNodeTypeCapture
}

func directorStepVerification(node *model.GraphNode) *model.DirectorInteractionVerification {
	if node == nil || len(node.Metadata) == 0 {
		return nil
	}
	status := firstNonEmptyString(artifactStringMetadata(node.Metadata, "verification_status"), artifactStringMetadata(node.Metadata, "status"))
	source := artifactStringMetadata(node.Metadata, "verification_source")
	verifiedID := artifactStringMetadata(node.Metadata, "verified_interaction_id")
	intentGoalID := artifactStringMetadata(node.Metadata, "intent_goal_id")
	runtimeAdaptive := boolFromAny(node.Metadata["runtime_adaptive"], false) || status == "runtime_adaptive" || strings.Contains(strings.ToLower(source), "runtime_adaptive")
	selectorScore := firstPositiveInt(node.Metadata["selector_score"])
	if status == "" && source == "" && verifiedID == "" && intentGoalID == "" && !runtimeAdaptive && selectorScore == 0 {
		return nil
	}
	authority := "verified_product_fact"
	guidance := "Treat this interaction as customer-side verified product evidence when planning narrative emphasis."
	if runtimeAdaptive {
		authority = "runtime_adaptive_executable_intent"
		guidance = "Treat this as executable intent that may resolve at runtime; do not present it as fully verified product proof unless captured execution artifacts confirm it."
	}
	return &model.DirectorInteractionVerification{
		Status:                status,
		Source:                source,
		VerifiedInteractionID: verifiedID,
		IntentGoalID:          intentGoalID,
		RuntimeAdaptive:       runtimeAdaptive,
		SelectorScore:         selectorScore,
		Authority:             authority,
		Guidance:              guidance,
	}
}

func directorStepIsRuntimeAdaptive(node *model.GraphNode) bool {
	verification := directorStepVerification(node)
	return verification != nil && verification.RuntimeAdaptive
}

func cloneNarrativeCue(value *model.NarrativeCue) *model.NarrativeCue {
	if value == nil {
		return nil
	}
	copy := *value
	if value.Timing != nil {
		timing := *value.Timing
		copy.Timing = &timing
	}
	return &copy
}

func cloneCaptureSpec(value *model.CaptureSpec) *model.CaptureSpec {
	if value == nil {
		return nil
	}
	copy := *value
	copy.MaskSelectors = append([]string{}, value.MaskSelectors...)
	copy.Redactions = append([]model.RedactionSpec{}, value.Redactions...)
	if value.Crop != nil {
		crop := *value.Crop
		copy.Crop = &crop
	}
	return &copy
}

func directorRecordingSummary(source *model.ClientExecutionPackage, recording *model.RecordingResultPackage) model.DirectorRecordingSummary {
	summary := model.DirectorRecordingSummary{
		RunID:             source.RecordingRunSpec.RunID,
		BaseURL:           source.RecordingRunSpec.BaseURL,
		TargetDurationSec: source.RecordingRunSpec.Timeline.TargetDurationSec,
		MaxDurationSec:    source.RecordingRunSpec.Timeline.MaxDurationSec,
		OutputFormats:     append([]string{}, source.RecordingRunSpec.Outputs.OutputFormats...),
		BrowserViewports:  append([]model.ViewportSpec{}, source.RecordingRunSpec.Browser.Viewports...),
		CaptureWindows:    append([]model.CaptureWindow{}, source.RecordingRunSpec.Timeline.CaptureWindows...),
		NodeTimingHints:   append([]model.NodeTimingHint{}, source.RecordingRunSpec.Timeline.NodeTimingHints...),
	}
	if source.RecordingRunSpec.Outputs.ResolutionWidth > 0 || source.RecordingRunSpec.Outputs.ResolutionHeight > 0 {
		summary.RequestedResolution = &model.DirectorResolution{
			Width:  source.RecordingRunSpec.Outputs.ResolutionWidth,
			Height: source.RecordingRunSpec.Outputs.ResolutionHeight,
		}
	}
	if recording != nil && recording.ExecutionTrace != nil {
		summary.PassRate = recording.ExecutionTrace.PassRate
		for _, step := range recording.ExecutionTrace.StepResults {
			summary.StepResults = append(summary.StepResults, model.DirectorStepResult{
				NodeID:        step.NodeID,
				Status:        step.Status,
				DurationMS:    step.DurationMS,
				ObservedState: step.ObservedState,
			})
		}
	}
	return summary
}

func directorMaterialsFrom(source *model.ClientExecutionPackage, recording *model.RecordingResultPackage, renderResult RenderResult) model.DirectorMaterialSet {
	materials := model.DirectorMaterialSet{}
	seen := map[string]bool{}
	for _, artifact := range append([]model.ArtifactRef{}, recording.GeneratedAssets...) {
		addDirectorMaterial(&materials, materialRefFromArtifact(artifact), seen)
	}
	if recording.ExecutionTrace != nil {
		for _, artifact := range recording.ExecutionTrace.Artifacts {
			addDirectorMaterial(&materials, materialRefFromArtifact(artifact), seen)
		}
		for _, step := range recording.ExecutionTrace.StepResults {
			for _, artifact := range step.Artifacts {
				addDirectorMaterial(&materials, materialRefFromArtifact(artifact), seen)
			}
		}
	}
	addRenderPathMaterial(&materials, seen, source, "final_demo_video", renderResult.VideoPath, "video/mp4", true)
	addRenderPathMaterial(&materials, seen, source, "source_reference_video", renderResult.SourceReferenceVideoPath, "video/mp4", false)
	addRenderPathMaterial(&materials, seen, source, "asset_timeline_catalog", renderResult.AssetTimelineCatalogPath, "application/json", false)
	addRenderPathMaterial(&materials, seen, source, "demo_edit_plan", renderResult.DemoEditPlanPath, "application/json", false)
	addRenderPathMaterial(&materials, seen, source, "render_manifest", renderResult.RenderManifestPath, "application/json", false)
	addRenderPathMaterial(&materials, seen, source, "media_normalization_report", renderResult.MediaNormalizationReportPath, "application/json", false)
	addRenderPathMaterial(&materials, seen, source, "requirement_satisfaction_report", renderResult.RequirementReportPath, "application/json", false)
	return materials
}

func addRenderPathMaterial(materials *model.DirectorMaterialSet, seen map[string]bool, source *model.ClientExecutionPackage, kind string, path string, mime string, include bool) {
	if strings.TrimSpace(path) == "" {
		return
	}
	ref := fileMaterialRef(artifactID(source.PackageID, kind, 1), kind, path, mime)
	ref.IncludeInDemo = include
	ref.AssetRole = kind
	addDirectorMaterial(materials, ref, seen)
}

func fileMaterialRef(id string, kind string, path string, mime string) model.DirectorMaterialRef {
	sha, size := localFileDigest(path)
	return model.DirectorMaterialRef{
		ID:        id,
		Kind:      kind,
		URI:       path,
		MimeType:  mimeTypeForPath(path, mime),
		SHA256:    sha,
		SizeBytes: size,
	}
}

func materialRefFromArtifact(artifact model.ArtifactRef) model.DirectorMaterialRef {
	include, _ := artifact.Metadata["include_in_demo"].(bool)
	assetRole, _ := artifact.Metadata["asset_role"].(string)
	return model.DirectorMaterialRef{
		ID:            artifact.ID,
		Kind:          artifact.Kind,
		URI:           artifact.URI,
		MimeType:      artifact.MimeType,
		SHA256:        artifact.SHA256,
		SizeBytes:     artifact.SizeBytes,
		SourceNodeID:  artifact.SourceNodeID,
		AssetRole:     assetRole,
		IncludeInDemo: include,
		Sensitive:     artifact.Sensitive,
		Metadata:      artifact.Metadata,
	}
}

func addDirectorMaterial(materials *model.DirectorMaterialSet, ref model.DirectorMaterialRef, seen map[string]bool) {
	if ref.URI == "" && ref.ID == "" {
		return
	}
	key := ref.ID
	if key == "" {
		key = ref.URI
	}
	if seen[key] {
		return
	}
	seen[key] = true
	materials.AllArtifacts = append(materials.AllArtifacts, ref)
	switch {
	case ref.Kind == "source_reference_video":
		ref.IncludeInDemo = false
		materials.SourceReferenceVideo = &ref
	case ref.Kind == "demo_video":
		ref.IncludeInDemo = true
		materials.FinalDemoVideo = &ref
	case ref.Kind == "raw_recording":
		materials.RawRecordings = append(materials.RawRecordings, ref)
	case ref.Kind == "screenshot" || strings.Contains(ref.Kind, "screenshot"):
		materials.Screenshots = append(materials.Screenshots, ref)
	case strings.HasSuffix(ref.MimeType, "json") || strings.Contains(ref.Kind, "manifest") || strings.Contains(ref.Kind, "report") || strings.Contains(ref.Kind, "plan") || strings.Contains(ref.Kind, "catalog"):
		materials.MetadataArtifacts = append(materials.MetadataArtifacts, ref)
	}
}

func directorRequestedOutput(source *model.ClientExecutionPackage) model.DirectorRequestedOutput {
	return model.DirectorRequestedOutput{
		PrimaryFormat:       firstMatchingFormat(source.RecordingRunSpec.Outputs.OutputFormats, "mp4", "webm"),
		ReferenceFormat:     "mp4/h264/aac/1920x1080/30fps/yuv420p",
		PreferredAspect:     "16:9",
		PreferredResolution: "1080p",
	}
}

func directorOpenQuestions(source *model.ClientExecutionPackage, renderResult RenderResult, materials model.DirectorMaterialSet) []string {
	questions := []string{}
	if renderResult.SourceReferenceVideoPath == "" {
		questions = append(questions, "source_reference.mp4 was not produced; Seedance video reference requires mp4/mov input.")
	}
	if materials.SourceReferenceVideo != nil && !isPublicHTTPSURL(materials.SourceReferenceVideo.URI) {
		questions = append(questions, "source_reference_video is local; real Ark calls require a public HTTPS URL, provider asset ID, or Base64 within request limits.")
	}
	if source.WorkflowGraph != nil && source.WorkflowGraph.Assets != nil && source.WorkflowGraph.Assets.TargetDurationSec > 15 {
		questions = append(questions, "Seedance 2.0 single generation is 4-15 seconds; longer demos must be composed from captured footage and optional short generated segments.")
	}
	if runtimeAdaptiveCount := runtimeAdaptiveWorkflowStepCount(source); runtimeAdaptiveCount > 0 {
		questions = append(questions, "Workflow includes "+strconv.Itoa(runtimeAdaptiveCount)+" runtime-adaptive interaction step(s); treat them as executable intent until captured artifacts confirm the final product state.")
	}
	if lowSelectorCount := lowSelectorScoreWorkflowStepCount(source); lowSelectorCount > 0 {
		questions = append(questions, "Workflow includes "+strconv.Itoa(lowSelectorCount)+" interaction step(s) with low selector confidence; prioritize captured execution results over model inference for those steps.")
	}
	return questions
}

func runtimeAdaptiveWorkflowStepCount(source *model.ClientExecutionPackage) int {
	if source == nil || source.WorkflowGraph == nil {
		return 0
	}
	count := 0
	for _, node := range source.WorkflowGraph.Nodes {
		if directorStepIsRuntimeAdaptive(node) {
			count++
		}
	}
	return count
}

func lowSelectorScoreWorkflowStepCount(source *model.ClientExecutionPackage) int {
	if source == nil || source.WorkflowGraph == nil {
		return 0
	}
	count := 0
	for _, node := range source.WorkflowGraph.Nodes {
		verification := directorStepVerification(node)
		if verification != nil && verification.SelectorScore > 0 && verification.SelectorScore < 60 {
			count++
		}
	}
	return count
}

func requiredStepOrder(source *model.ClientExecutionPackage) []string {
	if source == nil {
		return nil
	}
	if source.ExecutableScriptBundle != nil && source.ExecutableScriptBundle.ScriptManifest.StepNodeIDs != nil {
		return compactStrings(source.ExecutableScriptBundle.ScriptManifest.StepNodeIDs)
	}
	if source.ExecutableScriptBundle != nil && source.ExecutableScriptBundle.PlanJSON != nil {
		steps := append([]model.ScriptStep{}, source.ExecutableScriptBundle.PlanJSON.Steps...)
		out := make([]string, 0, len(steps))
		for _, step := range steps {
			out = append(out, step.NodeID)
		}
		return compactStrings(out)
	}
	if source.WorkflowGraph != nil {
		out := make([]string, 0, len(source.WorkflowGraph.Nodes))
		for _, node := range source.WorkflowGraph.Nodes {
			if node != nil {
				out = append(out, node.ID)
			}
		}
		return compactStrings(out)
	}
	return nil
}

func seedanceContentPreview(input model.DirectorInput) []map[string]any {
	text := seedanceDirectorPrompt(input)
	content := []map[string]any{{"type": "text", "text": text}}
	for _, ref := range seedanceInputRefs(input.Materials) {
		if strings.HasPrefix(ref.MimeType, "video/") {
			content = append(content, map[string]any{
				"type":      "video_url",
				"video_url": map[string]any{"url": ref.URI},
				"role":      "reference_video",
			})
			continue
		}
		if strings.HasPrefix(ref.MimeType, "image/") {
			content = append(content, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": ref.URI},
				"role":      "reference_image",
			})
		}
	}
	return content
}

func seedanceDirectorPrompt(input model.DirectorInput) string {
	parts := []string{
		"Use existing captured product material only.",
		"User requirements have highest priority, then the client execution script, then model presentation suggestions.",
		"Design non-authoritative director guidance for pacing, captions, callouts, zoom/pan hints, transitions, color grade, and music direction.",
		"Do not invent or replace product UI, browser actions, selectors, or workflow steps.",
	}
	if input.UserIntent.TargetAudience != "" {
		parts = append(parts, "Target audience: "+input.UserIntent.TargetAudience+".")
	}
	if input.UserIntent.TargetDurationSec > 0 {
		parts = append(parts, "Requested final demo duration: "+strconv.Itoa(input.UserIntent.TargetDurationSec)+" seconds.")
	}
	if requirements := directorRequirementTextList(input.UserIntent.Requirements, 8); len(requirements) > 0 {
		parts = append(parts, "Mandatory user/client requirements: "+strings.Join(requirements, "; ")+".")
	}
	if len(input.StorylinePolicy.RequiredStepOrder) > 0 {
		parts = append(parts, "Preserve required step order: "+strings.Join(input.StorylinePolicy.RequiredStepOrder, " -> ")+".")
	}
	if count := directorInputRuntimeAdaptiveStepCount(input); count > 0 {
		parts = append(parts, "There are "+strconv.Itoa(count)+" runtime-adaptive steps; treat them as executable intent until captured artifacts confirm the product state.")
	}
	return truncatePrompt(strings.Join(parts, " "), 1800)
}

func directorInputRuntimeAdaptiveStepCount(input model.DirectorInput) int {
	count := 0
	for _, step := range input.Workflow.Nodes {
		if step.Verification != nil && step.Verification.RuntimeAdaptive {
			count++
		}
	}
	return count
}

func seedanceInputRefs(materials model.DirectorMaterialSet) []model.DirectorMaterialRef {
	refs := []model.DirectorMaterialRef{}
	if materials.SourceReferenceVideo != nil {
		refs = append(refs, *materials.SourceReferenceVideo)
	}
	if len(refs) == 0 && len(materials.RawRecordings) > 0 {
		refs = append(refs, materials.RawRecordings[0])
	}
	for _, screenshot := range materials.Screenshots {
		if len(refs) >= 4 {
			break
		}
		refs = append(refs, screenshot)
	}
	return refs
}

func screenshotInputRefs(materials model.DirectorMaterialSet) []model.DirectorMaterialRef {
	limit := len(materials.Screenshots)
	if limit > 4 {
		limit = 4
	}
	return append([]model.DirectorMaterialRef{}, materials.Screenshots[:limit]...)
}

func seedreamPromptPreview(input model.DirectorInput) string {
	name := input.Project.ProductName
	if name == "" {
		name = "the recorded product"
	}
	styleRequirements := []string{}
	for _, requirement := range input.UserIntent.Requirements {
		switch requirement.Kind {
		case "style", "caption", "voiceover":
			styleRequirements = append(styleRequirements, requirement.Text)
		}
	}
	prompt := "Create a clean 16:9 enterprise demo title card or section divider inspired by the real captured product materials for " + name + ". Do not recreate product UI screens, do not add fake app content, and avoid readable fake interface text."
	if len(styleRequirements) > 0 {
		prompt += " Respect these user presentation requirements: " + strings.Join(compactStrings(styleRequirements), "; ") + "."
	}
	return truncatePrompt(prompt, 1000)
}

func firstMatchingFormat(values []string, preferred ...string) string {
	for _, want := range preferred {
		for _, value := range values {
			if strings.EqualFold(strings.TrimSpace(value), want) {
				return strings.ToLower(want)
			}
		}
	}
	if len(values) > 0 {
		return strings.ToLower(strings.TrimSpace(values[0]))
	}
	return "mp4"
}

func compactStrings(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func directorRequirementTextList(requirements []model.DirectorUserRequirement, limit int) []string {
	out := []string{}
	for _, requirement := range requirements {
		if strings.TrimSpace(requirement.Text) == "" {
			continue
		}
		out = append(out, requirement.Kind+": "+requirement.Text)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func truncatePrompt(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}

func mapFromAny(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out, true
	default:
		return nil, false
	}
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	default:
		return ""
	}
}

func stringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return compactStrings(typed)
	case []any:
		values := []string{}
		for _, item := range typed {
			values = append(values, stringFromAny(item))
		}
		return compactStrings(values)
	case string:
		return compactStrings([]string{typed})
	default:
		return nil
	}
}

func boolFromAny(value any, fallback bool) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "yes", "1", "required":
			return true
		case "false", "no", "0", "optional":
			return false
		default:
			return fallback
		}
	default:
		return fallback
	}
}

func firstPositiveInt(values ...any) int {
	for _, value := range values {
		switch typed := value.(type) {
		case int:
			if typed > 0 {
				return typed
			}
		case int32:
			if typed > 0 {
				return int(typed)
			}
		case int64:
			if typed > 0 {
				return int(typed)
			}
		case float32:
			if typed > 0 {
				return int(typed)
			}
		case float64:
			if typed > 0 {
				return int(typed)
			}
		case json.Number:
			parsed, err := typed.Int64()
			if err == nil && parsed > 0 {
				return int(parsed)
			}
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(typed))
			if err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return 0
}

func isPublicHTTPSURL(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "https://")
}

func isHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) && parsed.Host != ""
}

func mergeStrings(left []string, right []string) []string {
	return compactStrings(append(append([]string{}, left...), right...))
}

func joinEndpoint(baseURL string, endpoint string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(endpoint, "/")
}

func configuredArkMediaModeFromEnv() config.ArkMediaMode {
	mode := config.ArkMediaMode(strings.TrimSpace(os.Getenv("CASCADE_ARK_MEDIA_MODE")))
	switch mode {
	case config.ArkMediaModeDisabled, config.ArkMediaModeDryRun, config.ArkMediaModeReal:
		return mode
	default:
		return config.ArkMediaModeDryRun
	}
}

func arkMediaAPIKeyConfiguredFromEnv() bool {
	for _, key := range []string{"SEEDANCE_API_KEY", "DOUBAO_API_KEY", "ARK_API_KEY"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

func arkAssetPublisherFromEnv(now func() time.Time) media.AssetPublisher {
	publicDir := strings.TrimSpace(os.Getenv("CASCADE_ARK_ASSET_PUBLIC_DIR"))
	publicBaseURL := strings.TrimSpace(os.Getenv("CASCADE_ARK_ASSET_PUBLIC_BASE_URL"))
	if publicDir != "" || publicBaseURL != "" {
		return media.NewStaticAssetPublisher(publicDir, publicBaseURL, now)
	}
	return media.NewDryRunAssetPublisher(now)
}

func directorAdapterFromEnv(now func() time.Time) DirectorAdapter {
	mode := configuredArkMediaModeFromEnv()
	return NewConfiguredDirectorAdapter(DirectorAdapterOptions{
		ArkMediaMode:     mode,
		APIKeyConfigured: arkMediaAPIKeyConfiguredFromEnv(),
		ModeSource:       "CASCADE_ARK_MEDIA_MODE",
		Now:              now,
		ArkClient:        arkMediaClientFromEnv(mode),
	})
}

func arkMediaClientFromEnv(mode config.ArkMediaMode) media.ArkMediaClient {
	if mode != config.ArkMediaModeReal {
		return nil
	}
	return media.NewClient(arkMediaRuntimeFromEnv(mode), nil)
}

func arkMediaRuntimeFromEnv(mode config.ArkMediaMode) config.AppRuntimeConfig {
	seedanceKey, seedanceSource := envWithFallbackTrimmed("SEEDANCE_API_KEY", "DOUBAO_API_KEY", "ARK_API_KEY")
	seedreamKey, seedreamSource := envWithFallbackTrimmed("SEEDREAM_API_KEY", "DOUBAO_API_KEY", "ARK_API_KEY")
	doubaoKey, doubaoSource := envWithFallbackTrimmed("DOUBAO_API_KEY", "ARK_API_KEY")
	return config.AppRuntimeConfig{
		ArkMediaMode: mode,
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderSeedance: {
				Provider:           config.ModelProviderSeedance,
				APIKey:             seedanceKey,
				APIKeyEnv:          "SEEDANCE_API_KEY",
				APIKeySourceEnv:    seedanceSource,
				APIKeyFallbackEnvs: []string{"DOUBAO_API_KEY", "ARK_API_KEY"},
				BaseURL:            envOrDefaultTrimmed("SEEDANCE_BASE_URL", defaultArkBaseURL),
				BaseURLEnv:         "SEEDANCE_BASE_URL",
				DefaultModel:       envOrDefaultTrimmed("SEEDANCE_MODEL", defaultSeedanceModel),
				DefaultModelEnv:    "SEEDANCE_MODEL",
				Enabled:            seedanceKey != "",
			},
			config.ModelProviderSeedream: {
				Provider:           config.ModelProviderSeedream,
				APIKey:             seedreamKey,
				APIKeyEnv:          "SEEDREAM_API_KEY",
				APIKeySourceEnv:    seedreamSource,
				APIKeyFallbackEnvs: []string{"DOUBAO_API_KEY", "ARK_API_KEY"},
				BaseURL:            envOrDefaultTrimmed("SEEDREAM_BASE_URL", defaultArkBaseURL),
				BaseURLEnv:         "SEEDREAM_BASE_URL",
				DefaultModel:       envOrDefaultTrimmed("SEEDREAM_MODEL", defaultSeedreamModel),
				DefaultModelEnv:    "SEEDREAM_MODEL",
				Enabled:            seedreamKey != "",
			},
			config.ModelProviderDoubao: {
				Provider:           config.ModelProviderDoubao,
				APIKey:             doubaoKey,
				APIKeyEnv:          "DOUBAO_API_KEY",
				APIKeySourceEnv:    doubaoSource,
				APIKeyFallbackEnvs: []string{"ARK_API_KEY"},
				BaseURL:            envOrDefaultTrimmed("DOUBAO_BASE_URL", defaultArkBaseURL),
				BaseURLEnv:         "DOUBAO_BASE_URL",
				DefaultModel:       envOrDefaultTrimmed("DOUBAO_MODEL", ""),
				DefaultModelEnv:    "DOUBAO_MODEL",
				Enabled:            doubaoKey != "",
			},
		},
	}
}

func envWithFallbackTrimmed(primary string, fallbacks ...string) (string, string) {
	if value := strings.TrimSpace(os.Getenv(primary)); value != "" {
		return value, primary
	}
	for _, fallback := range fallbacks {
		if value := strings.TrimSpace(os.Getenv(fallback)); value != "" {
			return value, fallback
		}
	}
	return "", ""
}

func envOrDefaultTrimmed(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func writeIndentedJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
