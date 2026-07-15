package executor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	publisher := media.NewDryRunAssetPublisher(func() time.Time { return createdAt })
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
	return nil
}

func NewDirectorInput(source *model.ClientExecutionPackage, recording *model.RecordingResultPackage, renderResult RenderResult, createdAt time.Time) model.DirectorInput {
	materials := directorMaterialsFrom(source, recording, renderResult)
	requiredStepOrder := requiredStepOrder(source)
	return model.DirectorInput{
		SchemaVersion:        model.DirectorInputSchemaVersion,
		DirectorInputID:      "director_input_" + safeID(source.PackageID),
		CreatedAt:            createdAt,
		SourcePackageID:      source.PackageID,
		RecordingResultID:    recording.ResultID,
		Project:              directorProjectContext(source),
		SourceAuthority:      model.DemoEditSourceAuthorityCustomerSideAgent,
		ModelRole:            model.DemoEditModelRolePresentationOptimizerOnly,
		SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly,
		StorylinePolicy: model.DirectorStorylinePolicy{
			PrimaryStorylineSource: "client_execution_package.workflow_graph_and_executable_script_bundle",
			PreserveStepOrder:      true,
			RequiredStepOrder:      requiredStepOrder,
		},
		ModelBoundaries: model.DirectorModelBoundaries{
			CanDo: []string{
				"optimize narrative pacing, captions, callouts, zoom/pan hints, transitions, color grade, and music direction",
				"select and reorder optional presentation emphasis only when required step order remains intact",
				"reference existing screenshots and recordings as the only product UI source of truth",
			},
			CannotDo: []string{
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
			Target:          target,
			ExpectedOutcome: node.ExpectedOutcome,
			Required:        true,
			DurationMS:      node.DurationHintMS,
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

func directorRecordingSummary(source *model.ClientExecutionPackage, recording *model.RecordingResultPackage) model.DirectorRecordingSummary {
	summary := model.DirectorRecordingSummary{
		RunID:             source.RecordingRunSpec.RunID,
		BaseURL:           source.RecordingRunSpec.BaseURL,
		TargetDurationSec: source.RecordingRunSpec.Timeline.TargetDurationSec,
		MaxDurationSec:    source.RecordingRunSpec.Timeline.MaxDurationSec,
		OutputFormats:     append([]string{}, source.RecordingRunSpec.Outputs.OutputFormats...),
		CaptureWindows:    append([]model.CaptureWindow{}, source.RecordingRunSpec.Timeline.CaptureWindows...),
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
	return questions
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
	text := "Use existing captured product material only. Design a short non-authoritative director preview for pacing, transitions, captions, and music direction. Do not invent or replace product UI."
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
	return "Create a clean 16:9 enterprise demo title card or section divider inspired by the real captured product materials for " + name + ". Do not recreate product UI screens, do not add fake app content, and avoid readable fake interface text."
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

func isPublicHTTPSURL(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "https://")
}

func mergeStrings(left []string, right []string) []string {
	return compactStrings(append(append([]string{}, left...), right...))
}

func joinEndpoint(baseURL string, endpoint string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(endpoint, "/")
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
