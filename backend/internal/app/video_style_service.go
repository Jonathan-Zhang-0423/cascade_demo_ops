package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

// ListVideoStyleTemplates returns platform-owned templates. These are
// presentation recipes, not customer footage or provider prompts.
func (s *Service) ListVideoStyleTemplates(_ context.Context) ([]model.VideoStyleTemplate, error) {
	return cloneVideoStyleTemplates(builtInVideoStyleTemplates()), nil
}

// CreateEditorStyleDraft compiles a selected template into a separate
// reviewable plan. It never changes the active edit plan or original material.
func (s *Service) CreateEditorStyleDraft(ctx context.Context, sessionID string, request model.EditorStyleDraftRequest) (model.EditorStyleDraft, error) {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	if s.editorWorker == nil {
		return model.EditorStyleDraft{}, errors.New("editor worker is unavailable")
	}
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		return model.EditorStyleDraft{}, err
	}
	if request.ExpectedRevision != session.Revision {
		return model.EditorStyleDraft{}, fmt.Errorf("editor revision conflict: expected %d, current %d", request.ExpectedRevision, session.Revision)
	}
	template, err := findVideoStyleTemplate(request.TemplateID)
	if err != nil {
		return model.EditorStyleDraft{}, err
	}
	reference, warnings, err := styleReferenceForSession(session, request)
	if err != nil {
		return model.EditorStyleDraft{}, err
	}
	draftID, err := newEditorID("style")
	if err != nil {
		return model.EditorStyleDraft{}, err
	}
	now := time.Now().UTC()
	profile := styleProfileForDraft(draftID, template, reference, now)
	if reference != nil {
		warnings = append(warnings, model.VideoStyleDraftWarning{
			Code:    "reference_analysis_pending",
			Message: "参考视频已登记为风格输入；当前版本只使用所选平台模板。自动提取分镜、节奏、字幕和运镜特征将在风格分析 Worker 接入后启用。",
		})
	}
	plan, previewProfile, finalProfile, compileWarnings := compileStyleTemplate(session, template, request.Prompt, draftID)
	warnings = append(warnings, compileWarnings...)
	validation, validateErr := s.editorWorker.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{
		Catalog:  session.AssetCatalog,
		EditPlan: plan,
	})
	if validateErr != nil {
		return model.EditorStyleDraft{}, validateErr
	}
	draft := model.EditorStyleDraft{
		SchemaVersion:          model.VideoStyleDraftSchemaVersion,
		DraftID:                draftID,
		SessionID:              session.SessionID,
		BaseRevision:           session.Revision,
		CreatedAt:              now,
		Prompt:                 strings.TrimSpace(request.Prompt),
		Template:               template,
		StyleProfile:           profile,
		ProposedEditPlan:       plan,
		ProposedPreviewProfile: previewProfile,
		ProposedFinalProfile:   finalProfile,
		Validation:             &validation,
		RequiresConfirmation:   true,
		Warnings:               warnings,
	}
	if err := s.saveEditorStyleDraftUnlocked(draft); err != nil {
		return model.EditorStyleDraft{}, err
	}
	return draft, nil
}

func (s *Service) GetEditorStyleDraft(_ context.Context, sessionID, draftID string) (model.EditorStyleDraft, error) {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	if _, err := s.loadEditorSession(sessionID); err != nil {
		return model.EditorStyleDraft{}, err
	}
	return s.loadEditorStyleDraftUnlocked(sessionID, draftID)
}

// ApplyEditorStyleDraft is explicit and revision-protected. It applies only a
// worker-validated proposal, so a stale response cannot overwrite a newer edit.
func (s *Service) ApplyEditorStyleDraft(ctx context.Context, sessionID, draftID string, request model.EditorApplyStyleDraftRequest) (model.EditorSession, error) {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	if s.editorWorker == nil {
		return model.EditorSession{}, errors.New("editor worker is unavailable")
	}
	session, err := s.loadEditorSession(sessionID)
	if err != nil {
		return model.EditorSession{}, err
	}
	if request.ExpectedRevision != session.Revision {
		return model.EditorSession{}, fmt.Errorf("editor revision conflict: expected %d, current %d", request.ExpectedRevision, session.Revision)
	}
	draft, err := s.loadEditorStyleDraftUnlocked(sessionID, draftID)
	if err != nil {
		return model.EditorSession{}, err
	}
	if draft.BaseRevision != session.Revision {
		return model.EditorSession{}, fmt.Errorf("style draft is stale: base revision %d, current %d", draft.BaseRevision, session.Revision)
	}
	if !draft.RequiresConfirmation {
		return model.EditorSession{}, errors.New("style draft is not eligible for explicit apply")
	}
	validation, err := s.editorWorker.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{
		Catalog:  session.AssetCatalog,
		EditPlan: draft.ProposedEditPlan,
	})
	if err != nil {
		return model.EditorSession{}, err
	}
	if !validation.Valid {
		return model.EditorSession{}, errors.New("style draft edit plan validation failed")
	}
	session.EditPlan = cloneEditorStylePlan(draft.ProposedEditPlan)
	session.PreviewProfile = draft.ProposedPreviewProfile
	session.FinalProfile = draft.ProposedFinalProfile
	session.Validation = &validation
	session.Revision++
	session.Status = model.EditorSessionStatusEditing
	session.UpdatedAt = time.Now().UTC()
	session.Preview = model.EditorRenderState{Status: model.EditorRenderStatusNotStarted}
	session.FinalRender = model.EditorRenderState{Status: model.EditorRenderStatusNotStarted}
	if err := s.saveEditorSessionUnlocked(session); err != nil {
		return model.EditorSession{}, err
	}
	return session, nil
}

func styleReferenceForSession(session model.EditorSession, request model.EditorStyleDraftRequest) (*model.VideoStyleReference, []model.VideoStyleDraftWarning, error) {
	assetID := strings.TrimSpace(request.ReferenceAssetID)
	if assetID == "" {
		return nil, nil, nil
	}
	if !request.ReferenceRightsConfirmed {
		return nil, nil, errors.New("reference video usage rights must be confirmed before style analysis")
	}
	for _, artifact := range session.AssetCatalog.Artifacts {
		if artifact.ID != assetID {
			continue
		}
		if artifact.Sensitive {
			return nil, nil, errors.New("sensitive assets cannot be used as a style reference")
		}
		if artifact.Kind != "style_reference_video" || artifact.AssetRole != "style_reference_only" || artifact.IncludeInDemo {
			return nil, nil, errors.New("style reference must be imported through the dedicated style-reference path")
		}
		return &model.VideoStyleReference{
				AssetID:            artifact.ID,
				SHA256:             artifact.SHA256,
				UserDeclaredRights: true,
				ContentCopied:      false,
			}, []model.VideoStyleDraftWarning{{
				Code:    "reference_content_not_copied",
				Message: "参考视频只用于提取可解释的制作参数；系统不会复制其中的画面、人物、品牌标识、文案、音乐或镜头。",
			}}, nil
	}
	return nil, nil, fmt.Errorf("style reference asset %s was not found in the editor session", assetID)
}

func styleProfileForDraft(draftID string, template model.VideoStyleTemplate, reference *model.VideoStyleReference, now time.Time) model.VideoStyleProfile {
	source := "platform_template"
	status := "template_ready"
	confidence := 1.0
	if reference != nil {
		source = "reference_video_pending_analysis"
		status = "pending_analysis"
		confidence = 0
	}
	return model.VideoStyleProfile{
		SchemaVersion:         model.VideoStyleProfileSchemaVersion,
		ProfileID:             "style_profile_" + draftID,
		Source:                source,
		AnalysisStatus:        status,
		RecommendedTemplateID: template.TemplateID,
		Reference:             reference,
		Presentation:          template.Presentation,
		Output:                template.Output,
		Confidence:            confidence,
		UnsupportedFeatures: []model.VideoStyleFeatureStatus{
			{Feature: "complex_camera_motion", Status: "not_rendered", Reason: "当前确定性渲染器不会把复杂运镜烧录到像素中。"},
			{Feature: "complex_animation", Status: "not_rendered", Reason: "当前确定性渲染器只保证已实现的裁剪、字幕、音频和静态素材能力。"},
		},
		CreatedAt: now,
	}
}

func compileStyleTemplate(session model.EditorSession, template model.VideoStyleTemplate, prompt, draftID string) (model.DemoEditPlan, model.EditorRenderProfile, model.EditorRenderProfile, []model.VideoStyleDraftWarning) {
	plan := cloneEditorStylePlan(session.EditPlan)
	plan.PlanID = "plan_" + draftID
	plan.Objective = firstNonEmptyString(strings.TrimSpace(prompt), template.Name, session.EditPlan.Objective)
	plan.GlobalStyle = &model.DemoEditGlobalStyle{
		ColorGrade:      template.Presentation.ColorDirection,
		Pacing:          template.Presentation.Pacing,
		TransitionStyle: template.Presentation.TransitionStyle,
	}
	plan.Audio = &model.DemoEditAudioPolicy{
		Mode:          "source",
		VolumePercent: template.Presentation.SourceVolumePercent,
	}
	actualDuration := editorPlanDuration(plan)
	plan.TargetDurationMS = actualDuration
	preview, final := profilesForVideoStyle(template, session.PreviewProfile, session.FinalProfile)
	warnings := []model.VideoStyleDraftWarning{{
		Code:    "style_effects_partially_rendered",
		Message: "当前渲染器会实际应用画幅、视频裁剪、字幕和音频设置；色彩方向、节奏标签和转场风格会被记录在计划中，但尚未全部烧录为像素效果。",
	}}
	if actualDuration < template.Output.MinDurationMS || actualDuration > template.Output.MaxDurationMS {
		warnings = append(warnings, model.VideoStyleDraftWarning{
			Code:    "material_duration_overrides_template",
			Message: "用户现有的真实业务素材时长与模板建议不完全一致；为保留必要业务步骤，成片时长以可用素材为准。",
		})
	}
	return plan, preview, final, warnings
}

func profilesForVideoStyle(template model.VideoStyleTemplate, currentPreview, currentFinal model.EditorRenderProfile) (model.EditorRenderProfile, model.EditorRenderProfile) {
	final := currentFinal
	final.Width = template.Output.Width
	final.Height = template.Output.Height
	final.FPS = template.Output.FPS
	if final.Format == "" {
		final.Format = "mp4"
	}
	preview := currentPreview
	preview.Width, preview.Height = previewDimensions(template.Output.Width, template.Output.Height)
	preview.FPS = template.Output.FPS
	if preview.Format == "" {
		preview.Format = "mp4"
	}
	return preview, final
}

func previewDimensions(width, height int) (int, int) {
	if width <= 0 || height <= 0 {
		return 1280, 720
	}
	if height > width {
		return 720, 1280
	}
	if width*3 == height*4 {
		return 960, 720
	}
	return 1280, 720
}

func (s *Service) saveEditorStyleDraftUnlocked(draft model.EditorStyleDraft) error {
	path, err := s.editorStyleDraftPath(draft.SessionID, draft.DraftID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		return err
	}
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func (s *Service) loadEditorStyleDraftUnlocked(sessionID, draftID string) (model.EditorStyleDraft, error) {
	path, err := s.editorStyleDraftPath(sessionID, draftID)
	if err != nil {
		return model.EditorStyleDraft{}, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return model.EditorStyleDraft{}, err
	}
	var draft model.EditorStyleDraft
	if err := json.Unmarshal(payload, &draft); err != nil {
		return model.EditorStyleDraft{}, err
	}
	if draft.SessionID != sessionID {
		return model.EditorStyleDraft{}, errors.New("style draft session does not match request")
	}
	return draft, nil
}

func (s *Service) editorStyleDraftPath(sessionID, draftID string) (string, error) {
	if _, err := s.editorSessionPath(sessionID); err != nil {
		return "", err
	}
	clean := strings.TrimSpace(draftID)
	if clean == "" || strings.ContainsAny(clean, "/\\") || strings.Contains(clean, "..") {
		return "", errors.New("invalid style draft id")
	}
	return filepath.Join(s.editorSessionsRoot(), sessionID, "style_drafts", clean+".json"), nil
}

func cloneEditorStylePlan(plan model.DemoEditPlan) model.DemoEditPlan {
	out := plan
	out.LockedFields = append([]string{}, plan.LockedFields...)
	out.ModelEditableFields = append([]string{}, plan.ModelEditableFields...)
	out.Shots = make([]model.DemoEditShot, 0, len(plan.Shots))
	for _, shot := range plan.Shots {
		copyShot := shot
		if shot.SourceTimeRangeMS != nil {
			timeRange := *shot.SourceTimeRangeMS
			copyShot.SourceTimeRangeMS = &timeRange
		}
		copyShot.Operations = append([]model.EditOperation{}, shot.Operations...)
		copyShot.Overlays = append([]model.EditOverlay{}, shot.Overlays...)
		out.Shots = append(out.Shots, copyShot)
	}
	if plan.GlobalStyle != nil {
		style := *plan.GlobalStyle
		out.GlobalStyle = &style
	}
	if plan.Audio != nil {
		audio := *plan.Audio
		audio.SplitPointsMS = append([]int{}, plan.Audio.SplitPointsMS...)
		audio.SegmentSettings = append([]model.DemoEditAudioSegmentSettings{}, plan.Audio.SegmentSettings...)
		out.Audio = &audio
	}
	out.Narrations = append([]model.DemoEditNarrationClip{}, plan.Narrations...)
	out.CaptionCues = append([]model.DemoEditCaptionCue{}, plan.CaptionCues...)
	return out
}

func builtInVideoStyleTemplates() []model.VideoStyleTemplate {
	return []model.VideoStyleTemplate{
		newVideoStyleTemplate(
			"concise_product_demo", "简洁产品演示", "先说明价值，再展示真实操作与结果，适合 B 端功能介绍。", "product_demo",
			model.VideoStyleOutput{AspectRatio: "16:9", Width: 1920, Height: 1080, FPS: 30, TargetDurationMS: 45000, MinDurationMS: 30000, MaxDurationMS: 60000},
			model.VideoStylePresentation{Pacing: "clear_and_direct", TransitionStyle: "simple_cut", ColorDirection: "neutral_product_ui", CaptionMode: "sparse", MaxCaptionChars: 18, SourceVolumePercent: 55, NarrationPreferred: true, AllowPresentationStill: true},
		),
		newVideoStyleTemplate(
			"fast_feature_demo", "快节奏功能亮点", "用较快节奏串联关键功能和结果，适合 30-45 秒短 Demo。", "product_demo",
			model.VideoStyleOutput{AspectRatio: "16:9", Width: 1920, Height: 1080, FPS: 30, TargetDurationMS: 40000, MinDurationMS: 25000, MaxDurationMS: 50000},
			model.VideoStylePresentation{Pacing: "fast_with_result_hold", TransitionStyle: "short_fade", ColorDirection: "cool_low_saturation", CaptionMode: "short", MaxCaptionChars: 16, SourceVolumePercent: 35, NarrationPreferred: true, AllowPresentationStill: true},
		),
		newVideoStyleTemplate(
			"guided_product_tutorial", "步骤讲解教程", "放慢节奏，按业务顺序解释操作和结果，适合培训与交付说明。", "tutorial",
			model.VideoStyleOutput{AspectRatio: "16:9", Width: 1920, Height: 1080, FPS: 30, TargetDurationMS: 75000, MinDurationMS: 45000, MaxDurationMS: 120000},
			model.VideoStylePresentation{Pacing: "guided_and_deliberate", TransitionStyle: "simple_cut", ColorDirection: "neutral_product_ui", CaptionMode: "explanatory", MaxCaptionChars: 22, SourceVolumePercent: 70, NarrationPreferred: true, AllowPresentationStill: true},
		),
		newVideoStyleTemplate(
			"vertical_product_short", "竖屏产品短视频", "采用竖屏画幅和短句字幕展示关键功能，适合移动端传播。", "short_form",
			model.VideoStyleOutput{AspectRatio: "9:16", Width: 1080, Height: 1920, FPS: 30, TargetDurationMS: 35000, MinDurationMS: 20000, MaxDurationMS: 60000},
			model.VideoStylePresentation{Pacing: "fast_with_result_hold", TransitionStyle: "short_fade", ColorDirection: "bright_clean", CaptionMode: "prominent", MaxCaptionChars: 14, SourceVolumePercent: 30, NarrationPreferred: true, AllowPresentationStill: true},
		),
	}
}

func newVideoStyleTemplate(id, name, summary, category string, output model.VideoStyleOutput, presentation model.VideoStylePresentation) model.VideoStyleTemplate {
	return model.VideoStyleTemplate{
		SchemaVersion: model.VideoStyleTemplateSchemaVersion,
		TemplateID:    id,
		Name:          name,
		Summary:       summary,
		Category:      category,
		Output:        output,
		Story: []model.VideoStyleStorySlot{
			{SlotID: "opening", Role: "opening_hook", Purpose: "说明用户价值", Required: false, TargetDurationMS: 3000},
			{SlotID: "business_steps", Role: "verified_business_steps", Purpose: "按原顺序展示真实业务操作", Required: true, TargetDurationMS: output.TargetDurationMS - 9000},
			{SlotID: "result", Role: "verified_result", Purpose: "强调已录制的结果画面", Required: true, TargetDurationMS: 4500},
			{SlotID: "closing", Role: "closing_summary", Purpose: "总结用户价值", Required: false, TargetDurationMS: 1500},
		},
		Presentation: presentation,
		Constraints: model.VideoStyleTemplateConstraints{
			ExistingAssetsOnly:        true,
			PreserveRequiredStepOrder: true,
			AllowedOperations:         []string{"trim", "caption", "volume", "mute", "narration", "insert_presentation_still"},
			UnavailableEffects:        []string{"complex_camera_motion", "complex_animation", "reference_content_copy"},
		},
	}
}

func findVideoStyleTemplate(templateID string) (model.VideoStyleTemplate, error) {
	templateID = strings.TrimSpace(templateID)
	templates := builtInVideoStyleTemplates()
	if templateID == "" {
		return cloneVideoStyleTemplate(templates[0]), nil
	}
	for _, template := range templates {
		if template.TemplateID == templateID {
			return cloneVideoStyleTemplate(template), nil
		}
	}
	return model.VideoStyleTemplate{}, fmt.Errorf("unknown video style template %q", templateID)
}

func cloneVideoStyleTemplates(values []model.VideoStyleTemplate) []model.VideoStyleTemplate {
	out := make([]model.VideoStyleTemplate, 0, len(values))
	for _, value := range values {
		out = append(out, cloneVideoStyleTemplate(value))
	}
	return out
}

func cloneVideoStyleTemplate(value model.VideoStyleTemplate) model.VideoStyleTemplate {
	out := value
	out.Story = append([]model.VideoStyleStorySlot{}, value.Story...)
	out.Constraints.AllowedOperations = append([]string{}, value.Constraints.AllowedOperations...)
	out.Constraints.UnavailableEffects = append([]string{}, value.Constraints.UnavailableEffects...)
	return out
}
