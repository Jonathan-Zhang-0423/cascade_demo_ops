package executor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

const (
	defaultArkBaseURL      = "https://ark.cn-beijing.volces.com/api/v3"
	defaultSeedanceModel   = "doubao-seedance-2-0-260128"
	defaultSeedreamModel   = "doubao-seedream-5-0-pro-260628"
	seedanceTaskEndpoint   = "/contents/generations/tasks"
	seedreamImagesEndpoint = "/images/generations"
)

func enrichRenderResultForDirector(source *model.ClientExecutionPackage, recording *model.RecordingResultPackage, renderResult *RenderResult, outputDir string, createdAt time.Time) error {
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
			InputRefs: seedanceInputRefs(input.Materials),
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
			InputRefs: screenshotInputRefs(input.Materials),
			OutputPolicy: model.ArkMediaOutputPolicy{
				DownloadImmediately: true,
				ExpectedFormats:     []string{"png", "jpeg"},
				URLTTLHours:         24,
				StoreAsArtifact:     true,
			},
		},
	}

	return model.ArkMediaDryRunPlan{
		SchemaVersion:    model.ArkMediaDryRunPlanSchemaVersion,
		PlanID:           "ark_media_dry_run_" + safeID(source.PackageID),
		CreatedAt:        createdAt,
		Mode:             "dry_run",
		Reason:           "Real Ark media calls are disabled until DirectorInput, public asset upload/download, cost controls, and source-only policy checks are explicitly enabled.",
		SourcePackageID:  source.PackageID,
		DirectorInputRef: directorRef,
		VideoProvider:    "seedance",
		VideoBaseURL:     videoBaseURL,
		VideoModel:       videoModel,
		ImageProvider:    "seedream",
		ImageBaseURL:     imageBaseURL,
		ImageModel:       imageModel,
		RecommendedTasks: tasks,
		RequiredBeforeRealCall: []string{
			"publish selected source_reference_video or screenshots as time-limited URLs accessible by Ark, or encode small references as Base64 within provider limits",
			"trim Seedance reference videos to 2-15 seconds each and no more than 15 seconds total",
			"download returned video_url, last_frame_url, or image URL artifacts within 24 hours",
			"validate generated media is non-authoritative and cannot replace captured product UI evidence",
			"record provider request/response metadata without persisting raw API keys",
		},
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
	if materials.SourceReferenceVideo != nil && !isPublicHTTPURL(materials.SourceReferenceVideo.URI) {
		questions = append(questions, "source_reference_video is local; real Ark calls require a public URL, provider asset ID, or Base64 within request limits.")
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

func isPublicHTTPURL(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
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
