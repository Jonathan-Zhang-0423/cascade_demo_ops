package executor

import (
	"context"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

const (
	directorAdapterDryRunName         = "dry_run_director_adapter"
	directorAdapterDisabledName       = "disabled_director_adapter"
	directorAdapterRealPreflightName  = "ark_real_preflight_director_adapter"
	directorAdapterArkSeedanceName    = "ark_seedance_director_adapter"
	directorAdapterCurrentVersion     = "0.2.0"
	directorProviderGateStatusDryRun  = "dry_run"
	directorProviderGateStatusOff     = "disabled"
	directorProviderGateStatusBlocked = "blocked_before_provider_call"
	directorProviderGateStatusReady   = "ready_for_real_call"
	directorProviderGateStatusCalled  = "provider_call_submitted"
	directorProviderGateStatusFailed  = "provider_call_failed"
)

type DirectorAdapter interface {
	Suggest(ctx context.Context, input model.DirectorInput, arkPlan model.ArkMediaDryRunPlan, publicationResult *model.ArkAssetPublicationResult) (model.DirectorEditSuggestion, model.DirectorEditSuggestionValidationReport, error)
}

type DryRunDirectorAdapter struct {
	now              func() time.Time
	arkMediaMode     config.ArkMediaMode
	apiKeyConfigured bool
	modeSource       string
	arkClient        media.ArkMediaClient
}

type DirectorAdapterOptions struct {
	ArkMediaMode     config.ArkMediaMode
	APIKeyConfigured bool
	ModeSource       string
	Now              func() time.Time
	ArkClient        media.ArkMediaClient
}

func NewDryRunDirectorAdapter(now func() time.Time) *DryRunDirectorAdapter {
	return NewConfiguredDirectorAdapter(DirectorAdapterOptions{
		ArkMediaMode: config.ArkMediaModeDryRun,
		ModeSource:   "constructor",
		Now:          now,
	})
}

func NewConfiguredDirectorAdapter(options DirectorAdapterOptions) *DryRunDirectorAdapter {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	mode := options.ArkMediaMode
	if mode == "" {
		mode = config.ArkMediaModeDryRun
	}
	return &DryRunDirectorAdapter{
		now:              now,
		arkMediaMode:     mode,
		apiKeyConfigured: options.APIKeyConfigured,
		modeSource:       firstNonEmptyString(options.ModeSource, "CASCADE_ARK_MEDIA_MODE"),
		arkClient:        options.ArkClient,
	}
}

func (a *DryRunDirectorAdapter) Suggest(ctx context.Context, input model.DirectorInput, arkPlan model.ArkMediaDryRunPlan, publicationResult *model.ArkAssetPublicationResult) (model.DirectorEditSuggestion, model.DirectorEditSuggestionValidationReport, error) {
	if err := ctx.Err(); err != nil {
		return model.DirectorEditSuggestion{}, model.DirectorEditSuggestionValidationReport{}, err
	}
	createdAt := a.now().UTC()
	gate := a.providerGate(arkPlan, publicationResult)
	providerCall, gate := a.maybeCallProvider(ctx, input, arkPlan, publicationResult, gate)
	suggestion := model.DirectorEditSuggestion{
		SchemaVersion:        model.DirectorEditSuggestionSchemaVersion,
		SuggestionID:         "director_suggestion_" + safeID(firstNonEmptyString(input.SourcePackageID, input.DirectorInputID)),
		CreatedAt:            createdAt,
		SourcePackageID:      input.SourcePackageID,
		DirectorInputID:      input.DirectorInputID,
		Mode:                 suggestionModeForGate(gate),
		Status:               suggestionStatusForGate(gate),
		Adapter:              a.adapterInfo(arkPlan, gate),
		DecisionPriority:     input.DecisionPriority,
		SourceMaterialPolicy: input.SourceMaterialPolicy,
		ModelRole:            input.ModelRole,
		Summary:              directorSuggestionSummary(input),
		RequirementHandling:  directorRequirementHandling(input),
		Narrative:            directorNarrativeBeats(input),
		ShotSuggestions:      directorShotSuggestions(input),
		CaptionSuggestions:   directorCaptionSuggestions(input),
		Style:                directorStyleSuggestion(input),
		Music:                directorMusicSuggestion(input),
		ProviderGate:         &gate,
		ProviderCall:         providerCall,
		SourceTraces:         input.UserIntent.SourceTraces,
		Notes:                directorAdapterNotes(input, gate),
	}
	validation := ValidateDirectorEditSuggestion(input, suggestion, createdAt)
	return suggestion, validation, nil
}

func (a *DryRunDirectorAdapter) adapterInfo(arkPlan model.ArkMediaDryRunPlan, gate model.DirectorProviderGate) model.DirectorAdapterInfo {
	return model.DirectorAdapterInfo{
		Name:         adapterNameForGate(gate),
		Version:      directorAdapterCurrentVersion,
		Provider:     adapterProviderForGate(gate),
		Model:        firstNonEmptyString(arkPlan.VideoModel, defaultSeedanceModel),
		RealCallMade: gate.RealCallMade,
	}
}

func (a *DryRunDirectorAdapter) providerGate(arkPlan model.ArkMediaDryRunPlan, publicationResult *model.ArkAssetPublicationResult) model.DirectorProviderGate {
	mode := a.arkMediaMode
	if mode == "" {
		mode = config.ArkMediaModeDryRun
	}
	gate := model.DirectorProviderGate{
		Mode:                   string(mode),
		Provider:               "seedance",
		Model:                  firstNonEmptyString(arkPlan.VideoModel, defaultSeedanceModel),
		CanAttemptRealCall:     false,
		RealCallMade:           false,
		ModeGate:               "CASCADE_ARK_MEDIA_MODE=real plus provider-ready source assets are required before any Ark request is sent",
		RequiredBeforeRealCall: append([]string{}, arkPlan.RequiredBeforeRealCall...),
		Blockers:               append([]model.ArkMediaReadinessFinding{}, arkPlan.RealCallReadiness.Blockers...),
		Warnings:               append([]model.ArkMediaReadinessFinding{}, arkPlan.RealCallReadiness.Warnings...),
	}
	switch mode {
	case config.ArkMediaModeDisabled:
		gate.Status = directorProviderGateStatusOff
		gate.Blockers = appendUniqueReadinessFindings(gate.Blockers, model.ArkMediaReadinessFinding{
			Code:    "ark_media_disabled",
			Message: "CASCADE_ARK_MEDIA_MODE=disabled; no Ark provider request may be sent",
		})
	case config.ArkMediaModeReal:
		gate.Status = directorProviderGateStatusBlocked
		if !a.apiKeyConfigured {
			gate.Blockers = appendUniqueReadinessFindings(gate.Blockers, model.ArkMediaReadinessFinding{
				Code:    "api_key_missing",
				Message: "Seedance API key is not configured through SEEDANCE_API_KEY, DOUBAO_API_KEY, or ARK_API_KEY",
			})
		}
		if !arkPlan.RealCallReadiness.CanCallWhenEnabled {
			gate.Blockers = appendUniqueReadinessFindings(gate.Blockers, model.ArkMediaReadinessFinding{
				Code:    "ark_plan_not_ready",
				Message: "Ark media dry-run plan is not ready for real calls",
			})
		}
		if publicationResult == nil {
			gate.Blockers = appendUniqueReadinessFindings(gate.Blockers, model.ArkMediaReadinessFinding{
				Code:    "publication_result_missing",
				Message: "Ark asset publication result is required before real provider calls",
			})
		} else {
			gate.Warnings = appendUniqueReadinessFindings(gate.Warnings, publicationResult.Warnings...)
			if !publicationResult.CanUseForRealCall {
				gate.Blockers = appendUniqueReadinessFindings(gate.Blockers, model.ArkMediaReadinessFinding{
					Code:    "publication_result_not_ready",
					Message: "Published source assets are not provider-ready; dry-run URLs or local files must not be used for real Ark calls",
				})
			}
			gate.Blockers = appendUniqueReadinessFindings(gate.Blockers, publicationResult.Blockers...)
		}
		if len(gate.Blockers) == 0 {
			gate.Status = directorProviderGateStatusReady
			gate.CanAttemptRealCall = true
		}
	default:
		gate.Status = directorProviderGateStatusDryRun
	}
	return gate
}

func (a *DryRunDirectorAdapter) maybeCallProvider(ctx context.Context, input model.DirectorInput, arkPlan model.ArkMediaDryRunPlan, publicationResult *model.ArkAssetPublicationResult, gate model.DirectorProviderGate) (*model.DirectorProviderCall, model.DirectorProviderGate) {
	if !gate.CanAttemptRealCall || a.arkClient == nil {
		return nil, gate
	}
	request, summary := seedanceProviderRequest(input, arkPlan, publicationResult)
	result, err := a.arkClient.CreateContentGenerationTask(ctx, request)
	call := directorProviderCallFromResult(result, summary, err)
	gate.RealCallMade = true
	if err == nil {
		gate.Status = directorProviderGateStatusCalled
		return &call, gate
	}
	gate.Status = directorProviderGateStatusFailed
	gate.Blockers = appendUniqueReadinessFindings(gate.Blockers, model.ArkMediaReadinessFinding{
		Code:    firstNonEmptyString(call.ErrorClass, "provider_call_failed"),
		Message: firstNonEmptyString(call.ErrorMessage, "Ark provider call failed"),
		TaskID:  "seedance_reference_director_preview",
	})
	return &call, gate
}

func seedanceProviderRequest(input model.DirectorInput, arkPlan model.ArkMediaDryRunPlan, publicationResult *model.ArkAssetPublicationResult) (media.ContentGenerationTaskRequest, model.DirectorProviderRequestSummary) {
	content := []media.ContentPart{{
		Type: "text",
		Text: seedanceDirectorPrompt(input),
	}}
	referenceURIs := []string{}
	for _, ref := range publishedSeedanceRefs(publicationResult) {
		if len(referenceURIs) >= 4 {
			break
		}
		referenceURIs = append(referenceURIs, ref.URI)
		switch {
		case strings.HasPrefix(ref.MimeType, "video/"):
			content = append(content, media.ContentPart{Type: "video_url", VideoURL: &media.MediaURL{URL: ref.URI}})
		case strings.HasPrefix(ref.MimeType, "image/"):
			content = append(content, media.ContentPart{Type: "image_url", ImageURL: &media.MediaURL{URL: ref.URI}})
		}
	}
	request := media.ContentGenerationTaskRequest{
		Model:           firstNonEmptyString(arkPlan.VideoModel, defaultSeedanceModel),
		Content:         content,
		Resolution:      "1080p",
		Ratio:           "16:9",
		Duration:        5,
		GenerateAudio:   false,
		ReturnLastFrame: true,
		Watermark:       false,
	}
	return request, model.DirectorProviderRequestSummary{
		ContentPartCount: len(content),
		ReferenceURIs:    referenceURIs,
		Resolution:       request.Resolution,
		Ratio:            request.Ratio,
		DurationSec:      request.Duration,
		GenerateAudio:    request.GenerateAudio,
		Watermark:        request.Watermark,
	}
}

func publishedSeedanceRefs(publicationResult *model.ArkAssetPublicationResult) []model.DirectorMaterialRef {
	if publicationResult == nil {
		return nil
	}
	refs := []model.DirectorMaterialRef{}
	for _, item := range publicationResult.Items {
		if !item.CanUseForRealCall || item.ProposedPublicRef == nil {
			continue
		}
		if !containsString(item.TaskIDs, "seedance_reference_director_preview") {
			continue
		}
		refs = append(refs, *item.ProposedPublicRef)
	}
	return refs
}

func directorProviderCallFromResult(result media.ContentGenerationTaskResult, summary model.DirectorProviderRequestSummary, err error) model.DirectorProviderCall {
	call := model.DirectorProviderCall{
		Operation:        "seedance_reference_director_preview",
		Status:           "submitted",
		Provider:         string(result.Provider),
		Model:            result.Model,
		RealCallMade:     true,
		RequestSummary:   summary,
		Trace:            directorProviderTrace(result.Trace),
		NonAuthoritative: true,
	}
	if result.Response != nil {
		call.TaskID = result.Response.ID
		call.ProviderStatus = result.Response.Status
		call.Output = result.Response.Output
		if result.Response.Error != nil {
			call.ErrorClass = firstNonEmptyString(result.Response.Error.Type, result.Response.Error.Code)
			call.ErrorMessage = result.Response.Error.Message
			call.Status = "provider_error"
		}
	}
	if err != nil {
		call.Status = "failed"
		call.ErrorClass = firstNonEmptyString(result.Trace.ErrorClass, "provider_call_failed")
		call.ErrorMessage = err.Error()
	}
	return call
}

func directorProviderTrace(trace media.ArkMediaCallTrace) model.DirectorProviderCallTrace {
	return model.DirectorProviderCallTrace{
		Method:       trace.Method,
		EndpointHost: trace.EndpointHost,
		EndpointPath: trace.EndpointPath,
		HTTPStatus:   trace.HTTPStatus,
		ErrorClass:   trace.ErrorClass,
		LatencyMS:    trace.LatencyMS,
	}
}

func adapterNameForGate(gate model.DirectorProviderGate) string {
	switch gate.Status {
	case directorProviderGateStatusOff:
		return directorAdapterDisabledName
	case directorProviderGateStatusCalled, directorProviderGateStatusFailed:
		return directorAdapterArkSeedanceName
	case directorProviderGateStatusBlocked, directorProviderGateStatusReady:
		return directorAdapterRealPreflightName
	default:
		return directorAdapterDryRunName
	}
}

func adapterProviderForGate(gate model.DirectorProviderGate) string {
	switch gate.Status {
	case directorProviderGateStatusOff:
		return "ark_disabled"
	case directorProviderGateStatusCalled, directorProviderGateStatusFailed:
		return "ark_seedance"
	case directorProviderGateStatusBlocked, directorProviderGateStatusReady:
		return "ark_preflight"
	default:
		return "ark_dry_run"
	}
}

func suggestionModeForGate(gate model.DirectorProviderGate) string {
	switch gate.Status {
	case directorProviderGateStatusOff:
		return "disabled"
	case directorProviderGateStatusCalled, directorProviderGateStatusFailed:
		return "real_provider"
	case directorProviderGateStatusBlocked, directorProviderGateStatusReady:
		return "real_preflight"
	default:
		return "dry_run"
	}
}

func suggestionStatusForGate(gate model.DirectorProviderGate) string {
	switch gate.Status {
	case directorProviderGateStatusOff:
		return "provider_disabled_deterministic_suggestion"
	case directorProviderGateStatusCalled:
		return "provider_call_submitted"
	case directorProviderGateStatusFailed:
		return "provider_call_failed"
	case directorProviderGateStatusBlocked:
		return "blocked_before_provider_call"
	case directorProviderGateStatusReady:
		return "ready_for_provider_call"
	default:
		return "suggested"
	}
}

func directorAdapterNotes(input model.DirectorInput, gate model.DirectorProviderGate) []string {
	notes := []string{
		"Suggestions are model-facing planning output and must be validated before becoming DemoEditPlan changes.",
		"Captured product UI remains the only authoritative source material.",
	}
	if count := directorInputRuntimeAdaptiveStepCount(input); count > 0 {
		notes = append(notes, "Runtime-adaptive interaction steps are executable intent only; confirm captured artifacts before treating them as product proof.")
	}
	switch gate.Status {
	case directorProviderGateStatusCalled:
		notes = append([]string{"Real Ark provider call was submitted; returned media remains a non-authoritative candidate and cannot replace captured product UI."}, notes...)
	case directorProviderGateStatusFailed:
		notes = append([]string{"Real Ark provider call failed after passing preflight; deterministic suggestions are still preserved for the render pipeline."}, notes...)
	case directorProviderGateStatusReady:
		notes = append([]string{"Real Ark provider call is preflight-ready, but this adapter did not send the request."}, notes...)
	case directorProviderGateStatusBlocked:
		notes = append([]string{"Real Ark provider call was requested but blocked by preflight gates; no provider request was sent."}, notes...)
	case directorProviderGateStatusOff:
		notes = append([]string{"Ark media mode is disabled; no provider request was sent."}, notes...)
	default:
		notes = append([]string{"Dry-run adapter only; no provider request was sent."}, notes...)
	}
	return notes
}

func directorSuggestionSummary(input model.DirectorInput) string {
	if count := directorInputRuntimeAdaptiveStepCount(input); count > 0 {
		return "Prepare director guidance from explicit user requirements and the client-provided script, while marking " + itoa(count) + " runtime-adaptive step(s) as executable intent until captured artifacts confirm the product state."
	}
	if input.UserIntent.Status == "manual_requirements_present" {
		return "Prepare director guidance that satisfies explicit user demo requirements first, while preserving the client-provided interaction script and source-only material policy."
	}
	return "Prepare director guidance from the client-provided interaction script and captured materials, keeping cloud model suggestions limited to presentation optimization."
}

func directorRequirementHandling(input model.DirectorInput) []model.DirectorRequirementHandling {
	out := []model.DirectorRequirementHandling{}
	for _, requirement := range input.UserIntent.Requirements {
		out = append(out, model.DirectorRequirementHandling{
			RequirementID:   requirement.ID,
			RequirementKind: requirement.Kind,
			Status:          "planned",
			Handling:        "Carry this requirement into model prompts and downstream DemoEditPlan validation. If unavailable in captured material, report unsatisfied instead of inventing UI.",
			PrioritySource:  requirement.Source,
			SourceTrace: []model.DirectorSourceTrace{{
				Source:     requirement.Source,
				FieldPath:  requirement.SourcePath,
				Confidence: "high",
			}},
		})
	}
	return out
}

func directorNarrativeBeats(input model.DirectorInput) []model.DirectorNarrativeBeat {
	beats := []model.DirectorNarrativeBeat{}
	if len(input.UserIntent.Requirements) > 0 {
		beats = append(beats, model.DirectorNarrativeBeat{
			ID:               "beat_user_intent",
			Title:            "User intent",
			Purpose:          "Open the demo by emphasizing the highest-priority user requirements.",
			RelatedStepIDs:   firstDirectorStepIDs(input, 1),
			TargetDurationMS: 2500,
			PrioritySource:   "user_explicit_requirements",
			SourceTrace:      input.UserIntent.SourceTraces,
		})
	}
	for _, step := range input.Workflow.Nodes {
		beats = append(beats, model.DirectorNarrativeBeat{
			ID:               "beat_" + safeID(step.NodeID),
			Title:            directorStepTitle(step),
			Purpose:          directorNarrativePurpose(step),
			RelatedStepIDs:   []string{step.NodeID},
			TargetDurationMS: positiveOrFallback(step.DurationMS, 1800),
			PrioritySource:   "client_execution_script",
			SourceTrace:      directorStepSourceTrace(step, ""),
		})
	}
	if len(beats) == 0 {
		beats = append(beats, model.DirectorNarrativeBeat{
			ID:               "beat_source_material",
			Purpose:          "Present the captured product material in a concise source-only demo flow.",
			TargetDurationMS: 3000,
			PrioritySource:   "client_execution_script",
		})
	}
	return beats
}

func directorShotSuggestions(input model.DirectorInput) []model.DirectorShotSuggestion {
	out := []model.DirectorShotSuggestion{}
	for index, step := range input.Workflow.Nodes {
		sourceArtifactID := sourceArtifactIDForStep(input.Materials, step.NodeID)
		out = append(out, model.DirectorShotSuggestion{
			ID:                "shot_" + safeID(step.NodeID),
			SourceStepID:      step.NodeID,
			SourceArtifactID:  sourceArtifactID,
			SourceTimeRangeMS: []int{0, positiveOrFallback(step.DurationMS, 1800)},
			Operation:         directorShotOperation(step),
			Purpose:           directorShotPurpose(step),
			PrioritySource:    "client_execution_script",
			SourceTrace:       directorStepSourceTrace(step, ""),
		})
		if index >= 11 {
			break
		}
	}
	return out
}

func directorCaptionSuggestions(input model.DirectorInput) []model.DirectorCaptionSuggestion {
	out := []model.DirectorCaptionSuggestion{}
	for _, requirement := range input.UserIntent.Requirements {
		if requirement.Kind != "caption" && requirement.Kind != "must_show" && requirement.Kind != "value_proposition" {
			continue
		}
		out = append(out, model.DirectorCaptionSuggestion{
			ID:             "caption_" + safeID(requirement.ID),
			Text:           requirement.Text,
			AnchorStepID:   firstString(requirement.NodeRefs),
			PrioritySource: requirement.Source,
			SourceTrace: []model.DirectorSourceTrace{{
				Source:     requirement.Source,
				FieldPath:  requirement.SourcePath,
				Confidence: "high",
			}},
		})
		if len(out) >= 8 {
			break
		}
	}
	if len(out) == 0 {
		for _, step := range input.Workflow.Nodes {
			text := directorCaptionTextForStep(step)
			if strings.TrimSpace(text) == "" {
				continue
			}
			out = append(out, model.DirectorCaptionSuggestion{
				ID:             "caption_" + safeID(step.NodeID),
				Text:           text,
				AnchorStepID:   step.NodeID,
				PrioritySource: "client_execution_script",
				SourceTrace:    directorStepSourceTrace(step, ".expected_outcome"),
			})
			if len(out) >= 4 {
				break
			}
		}
	}
	return out
}

func directorStyleSuggestion(input model.DirectorInput) model.DirectorStyleSuggestion {
	style := model.DirectorStyleSuggestion{
		Pacing:          "concise",
		TransitionStyle: "clean_cut_or_subtle_push",
		ColorGrade:      "neutral_enterprise",
		MotionStyle:     "source_material_zoom_and_pan_only",
		SourceTrace: []model.DirectorSourceTrace{{
			Source:     "cloud_model_suggestion",
			FieldPath:  "director_adapter.default_style",
			Confidence: "medium",
		}},
	}
	for _, requirement := range input.UserIntent.Requirements {
		if requirement.Kind != "style" {
			continue
		}
		style.Pacing = "user_directed"
		style.MotionStyle = requirement.Text
		style.SourceTrace = []model.DirectorSourceTrace{{
			Source:     requirement.Source,
			FieldPath:  requirement.SourcePath,
			Confidence: "high",
		}}
		break
	}
	return style
}

func directorMusicSuggestion(input model.DirectorInput) model.DirectorMusicSuggestion {
	trace := []model.DirectorSourceTrace{{
		Source:     "cloud_model_suggestion",
		FieldPath:  "director_adapter.music_direction",
		Confidence: "medium",
	}}
	direction := "restrained enterprise background music; keep product interaction audio deterministic until user review"
	for _, requirement := range input.UserIntent.Requirements {
		if requirement.Kind != "voiceover" {
			continue
		}
		direction = requirement.Text
		trace = []model.DirectorSourceTrace{{
			Source:     requirement.Source,
			FieldPath:  requirement.SourcePath,
			Confidence: "high",
		}}
		break
	}
	return model.DirectorMusicSuggestion{
		Direction:      direction,
		GenerateAudio:  false,
		PrioritySource: "cloud_model_suggestion",
		SourceTrace:    trace,
	}
}

func ValidateDirectorEditSuggestion(input model.DirectorInput, suggestion model.DirectorEditSuggestion, checkedAt time.Time) model.DirectorEditSuggestionValidationReport {
	report := model.DirectorEditSuggestionValidationReport{
		SchemaVersion: model.DirectorEditSuggestionValidationSchemaVersion,
		SuggestionID:  suggestion.SuggestionID,
		Valid:         true,
		CheckedAt:     checkedAt.UTC(),
		Policies: []string{
			"UserIntent > ClientScript > ModelSuggestion",
			"existing_assets_only",
			"presentation_optimizer_only",
			"preserve_required_step_order",
			"source_trace_required",
		},
	}
	runtimeAdaptiveSteps := runtimeAdaptiveStepSet(input)
	if len(runtimeAdaptiveSteps) > 0 {
		report.Policies = append(report.Policies, "runtime_adaptive_not_product_proof")
	}
	if suggestion.SchemaVersion != model.DirectorEditSuggestionSchemaVersion {
		report.Errors = append(report.Errors, directorSuggestionFinding("schema_version_mismatch", "director edit suggestion schema_version is invalid", "schema_version", "error"))
	}
	if suggestion.SourceMaterialPolicy != model.DemoEditSourceMaterialPolicyExistingAssetsOnly {
		report.Errors = append(report.Errors, directorSuggestionFinding("source_policy_violation", "director suggestion must use existing assets only", "source_material_policy", "error"))
	}
	if suggestion.ModelRole != model.DemoEditModelRolePresentationOptimizerOnly {
		report.Errors = append(report.Errors, directorSuggestionFinding("model_role_violation", "director suggestion must keep model role presentation_optimizer_only", "model_role", "error"))
	}
	if len(suggestion.DecisionPriority.ResolutionOrder) == 0 || suggestion.DecisionPriority.ResolutionOrder[0].Source != "user_explicit_requirements" {
		report.Errors = append(report.Errors, directorSuggestionFinding("priority_policy_violation", "director suggestion must preserve UserIntent > ClientScript > ModelSuggestion priority", "decision_priority.resolution_order", "error"))
	}
	if len(input.UserIntent.Requirements) > 0 && len(suggestion.RequirementHandling) == 0 {
		report.Errors = append(report.Errors, directorSuggestionFinding("missing_requirement_handling", "explicit user/client requirements must be represented in director suggestions", "requirement_handling", "error"))
	}
	if suggestion.ProviderGate != nil {
		if suggestion.ProviderGate.RealCallMade && !suggestion.ProviderGate.CanAttemptRealCall {
			report.Errors = append(report.Errors, directorSuggestionFinding("provider_gate_violation", "director adapter must not make a real provider call unless provider gate allows it", "provider_gate.real_call_made", "error"))
		}
		if suggestion.ProviderGate.RealCallMade != suggestion.Adapter.RealCallMade {
			report.Errors = append(report.Errors, directorSuggestionFinding("provider_trace_mismatch", "adapter real_call_made and provider_gate.real_call_made must match", "adapter.real_call_made", "error"))
		}
	}
	if suggestion.ProviderCall != nil {
		if !suggestion.ProviderCall.NonAuthoritative {
			report.Errors = append(report.Errors, directorSuggestionFinding("provider_output_authority_violation", "provider output must remain non-authoritative and cannot replace captured product UI", "provider_call.non_authoritative", "error"))
		}
		if !suggestion.ProviderCall.RealCallMade || !suggestion.Adapter.RealCallMade {
			report.Errors = append(report.Errors, directorSuggestionFinding("provider_call_trace_mismatch", "provider_call requires adapter.real_call_made=true", "provider_call.real_call_made", "error"))
		}
	}
	for index, shot := range suggestion.ShotSuggestions {
		if shot.SourceStepID != "" && !containsString(input.StorylinePolicy.RequiredStepOrder, shot.SourceStepID) {
			report.Errors = append(report.Errors, directorSuggestionFinding("unknown_source_step", "shot references a step outside required script order", "shot_suggestions["+itoa(index)+"].source_step_id", "error"))
		}
		if shot.Operation == "" {
			report.Errors = append(report.Errors, directorSuggestionFinding("missing_shot_operation", "shot suggestion is missing operation", "shot_suggestions["+itoa(index)+"].operation", "error"))
		}
		if len(shot.SourceTrace) == 0 {
			report.Warnings = append(report.Warnings, directorSuggestionFinding("missing_source_trace", "shot suggestion should include source trace", "shot_suggestions["+itoa(index)+"].source_trace", "warning"))
		}
		if runtimeAdaptiveSteps[shot.SourceStepID] {
			if shot.Operation == "emphasize_verified_source" {
				report.Warnings = append(report.Warnings, directorSuggestionFinding("runtime_adaptive_shot_overclaims_authority", "runtime-adaptive shot must not be described as verified source proof", "shot_suggestions["+itoa(index)+"].operation", "warning"))
			}
			if containsProductProofLanguage(shot.Purpose) {
				report.Warnings = append(report.Warnings, directorSuggestionFinding("runtime_adaptive_shot_overclaims_outcome", "runtime-adaptive shot purpose should stay conditional until captured artifacts confirm the result", "shot_suggestions["+itoa(index)+"].purpose", "warning"))
			}
			if sourceTraceHasHighConfidence(shot.SourceTrace) {
				report.Warnings = append(report.Warnings, directorSuggestionFinding("runtime_adaptive_source_trace_confidence", "runtime-adaptive source trace should not use high confidence product-proof wording", "shot_suggestions["+itoa(index)+"].source_trace", "warning"))
			}
		}
	}
	for index, caption := range suggestion.CaptionSuggestions {
		if strings.TrimSpace(caption.Text) == "" {
			report.Errors = append(report.Errors, directorSuggestionFinding("empty_caption", "caption suggestion text must not be empty", "caption_suggestions["+itoa(index)+"].text", "error"))
		}
		if strings.Contains(strings.ToLower(caption.PrioritySource), "model") && len(input.UserIntent.Requirements) > 0 {
			report.Warnings = append(report.Warnings, directorSuggestionFinding("model_caption_below_user_intent", "model-origin caption must not override explicit user requirements", "caption_suggestions["+itoa(index)+"].priority_source", "warning"))
		}
		if runtimeAdaptiveSteps[caption.AnchorStepID] && containsProductProofLanguage(caption.Text) {
			report.Warnings = append(report.Warnings, directorSuggestionFinding("runtime_adaptive_caption_overclaims_outcome", "runtime-adaptive caption should not claim a completed product outcome until captured artifacts confirm it", "caption_suggestions["+itoa(index)+"].text", "warning"))
		}
	}
	report.Valid = len(report.Errors) == 0
	return report
}

func directorStepTitle(step model.DirectorWorkflowStep) string {
	return firstNonEmptyString(step.Title, step.Goal, step.ExpectedOutcome, step.Action, step.NodeID)
}

func directorNarrativePurpose(step model.DirectorWorkflowStep) string {
	if directorWorkflowStepIsRuntimeAdaptive(step) {
		return "Prepare to capture the runtime-resolved interaction for " + directorStepTitle(step) + "; confirm the final product state from captured artifacts before making outcome claims."
	}
	if directorWorkflowStepIsVerified(step) {
		return firstNonEmptyString(step.ExpectedOutcome, "Show the customer-side verified product interaction for "+step.NodeID)
	}
	return firstNonEmptyString(step.ExpectedOutcome, "Present the captured product interaction for "+step.NodeID)
}

func directorShotOperation(step model.DirectorWorkflowStep) string {
	if directorWorkflowStepIsRuntimeAdaptive(step) {
		return "capture_runtime_adaptive_intent"
	}
	if directorWorkflowStepIsVerified(step) {
		return "emphasize_verified_source"
	}
	return "emphasize_existing_source"
}

func directorShotPurpose(step model.DirectorWorkflowStep) string {
	if directorWorkflowStepIsRuntimeAdaptive(step) {
		return "Capture the runtime-resolved action for " + directorStepTitle(step) + " and keep the result conditional until the recording proves it."
	}
	if directorWorkflowStepIsVerified(step) {
		return firstNonEmptyString(step.ExpectedOutcome, "Highlight customer-side verified step "+step.NodeID)
	}
	return firstNonEmptyString(step.ExpectedOutcome, "Highlight captured step "+step.NodeID)
}

func directorCaptionTextForStep(step model.DirectorWorkflowStep) string {
	if directorWorkflowStepIsRuntimeAdaptive(step) {
		return "Capture the resolved action: " + directorStepTitle(step)
	}
	return step.ExpectedOutcome
}

func directorStepSourceTrace(step model.DirectorWorkflowStep, suffix string) []model.DirectorSourceTrace {
	source := "client_workflow_graph"
	confidence := "high"
	if step.Verification != nil {
		if step.Verification.Source != "" {
			source = step.Verification.Source
		}
		if step.Verification.RuntimeAdaptive || step.Verification.Authority == "runtime_adaptive_executable_intent" {
			confidence = "medium"
		}
		if step.Verification.SelectorScore > 0 && step.Verification.SelectorScore < 60 {
			confidence = "low"
		}
	}
	return []model.DirectorSourceTrace{{
		Source:     source,
		FieldPath:  "workflow.nodes." + step.NodeID + suffix,
		Confidence: confidence,
	}}
}

func directorWorkflowStepIsVerified(step model.DirectorWorkflowStep) bool {
	return step.Verification != nil && step.Verification.Authority == "verified_product_fact" && !step.Verification.RuntimeAdaptive
}

func directorWorkflowStepIsRuntimeAdaptive(step model.DirectorWorkflowStep) bool {
	return step.Verification != nil && (step.Verification.RuntimeAdaptive || step.Verification.Authority == "runtime_adaptive_executable_intent")
}

func runtimeAdaptiveStepSet(input model.DirectorInput) map[string]bool {
	out := map[string]bool{}
	for _, step := range input.Workflow.Nodes {
		if directorWorkflowStepIsRuntimeAdaptive(step) {
			out[step.NodeID] = true
		}
	}
	return out
}

func sourceTraceHasHighConfidence(traces []model.DirectorSourceTrace) bool {
	for _, trace := range traces {
		if strings.EqualFold(trace.Confidence, "high") {
			return true
		}
	}
	return false
}

func containsProductProofLanguage(value string) bool {
	lower := strings.ToLower(value)
	for _, token := range []string{"verified", "proof", "confirmed", "completed", "complete", "succeeded", "success"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func directorSuggestionFinding(code string, message string, path string, severity string) model.DirectorSuggestionFinding {
	return model.DirectorSuggestionFinding{Code: code, Message: message, Path: path, Severity: severity}
}

func sourceArtifactIDForStep(materials model.DirectorMaterialSet, nodeID string) string {
	for _, screenshot := range materials.Screenshots {
		if screenshot.SourceNodeID == nodeID && screenshot.ID != "" {
			return screenshot.ID
		}
	}
	for _, recording := range materials.RawRecordings {
		if recording.ID != "" {
			return recording.ID
		}
	}
	if materials.SourceReferenceVideo != nil {
		return materials.SourceReferenceVideo.ID
	}
	return ""
}

func firstDirectorStepIDs(input model.DirectorInput, limit int) []string {
	out := []string{}
	for _, step := range input.Workflow.Nodes {
		if step.NodeID == "" {
			continue
		}
		out = append(out, step.NodeID)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func positiveOrFallback(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func firstString(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func appendUniqueReadinessFindings(left []model.ArkMediaReadinessFinding, right ...model.ArkMediaReadinessFinding) []model.ArkMediaReadinessFinding {
	seen := map[string]bool{}
	out := append([]model.ArkMediaReadinessFinding{}, left...)
	for _, finding := range out {
		seen[readinessFindingKey(finding)] = true
	}
	for _, finding := range right {
		key := readinessFindingKey(finding)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, finding)
	}
	return out
}

func readinessFindingKey(finding model.ArkMediaReadinessFinding) string {
	return finding.Code + "|" + finding.RefID + "|" + finding.TaskID + "|" + finding.Message
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	const digits = "0123456789"
	out := ""
	for value > 0 {
		out = string(digits[value%10]) + out
		value = value / 10
	}
	return out
}
