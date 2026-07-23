package model

import "time"

const VideoStyleTemplateSchemaVersion = "demoops.video_style_template.v1"
const VideoStyleProfileSchemaVersion = "demoops.video_style_profile.v1"
const VideoStyleDraftSchemaVersion = "demoops.video_style_draft.v1"

// VideoStyleTemplate is a platform-owned, reviewable presentation recipe. It
// describes how to organize existing material; it never contains customer
// product footage, browser actions, or provider generation prompts.
type VideoStyleTemplate struct {
	SchemaVersion string                        `json:"schema_version"`
	TemplateID    string                        `json:"template_id"`
	Name          string                        `json:"name"`
	Summary       string                        `json:"summary"`
	Category      string                        `json:"category"`
	Output        VideoStyleOutput              `json:"output"`
	Story         []VideoStyleStorySlot         `json:"story"`
	Presentation  VideoStylePresentation        `json:"presentation"`
	Constraints   VideoStyleTemplateConstraints `json:"constraints"`
}

type VideoStyleOutput struct {
	AspectRatio      string `json:"aspect_ratio"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	FPS              int    `json:"fps"`
	TargetDurationMS int    `json:"target_duration_ms"`
	MinDurationMS    int    `json:"min_duration_ms"`
	MaxDurationMS    int    `json:"max_duration_ms"`
}

// VideoStyleStorySlot is deliberately semantic. The production agent maps it
// to verified project material later, rather than a template naming a product
// route or a customer-specific action.
type VideoStyleStorySlot struct {
	SlotID           string `json:"slot_id"`
	Role             string `json:"role"`
	Purpose          string `json:"purpose"`
	Required         bool   `json:"required"`
	TargetDurationMS int    `json:"target_duration_ms"`
}

type VideoStylePresentation struct {
	Pacing                 string `json:"pacing"`
	TransitionStyle        string `json:"transition_style"`
	ColorDirection         string `json:"color_direction"`
	CaptionMode            string `json:"caption_mode"`
	MaxCaptionChars        int    `json:"max_caption_chars"`
	SourceVolumePercent    int    `json:"source_volume_percent"`
	NarrationPreferred     bool   `json:"narration_preferred"`
	AllowPresentationStill bool   `json:"allow_presentation_still"`
}

type VideoStyleTemplateConstraints struct {
	ExistingAssetsOnly        bool     `json:"existing_assets_only"`
	PreserveRequiredStepOrder bool     `json:"preserve_required_step_order"`
	AllowedOperations         []string `json:"allowed_operations"`
	UnavailableEffects        []string `json:"unavailable_effects,omitempty"`
}

// VideoStyleProfile is the normalized result of a user-selected template or a
// future reference-video analysis. Reference video content is never copied
// into the final video; only explainable presentation parameters may be used.
type VideoStyleProfile struct {
	SchemaVersion         string                    `json:"schema_version"`
	ProfileID             string                    `json:"profile_id"`
	Source                string                    `json:"source"`
	AnalysisStatus        string                    `json:"analysis_status"`
	RecommendedTemplateID string                    `json:"recommended_template_id,omitempty"`
	Reference             *VideoStyleReference      `json:"reference,omitempty"`
	Presentation          VideoStylePresentation    `json:"presentation"`
	Output                VideoStyleOutput          `json:"output"`
	Confidence            float64                   `json:"confidence,omitempty"`
	UnsupportedFeatures   []VideoStyleFeatureStatus `json:"unsupported_features,omitempty"`
	CreatedAt             time.Time                 `json:"created_at"`
}

type VideoStyleReference struct {
	AssetID            string `json:"asset_id"`
	SHA256             string `json:"sha256,omitempty"`
	UserDeclaredRights bool   `json:"user_declared_rights"`
	ContentCopied      bool   `json:"content_copied"`
}

type VideoStyleFeatureStatus struct {
	Feature string `json:"feature"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
}

type EditorStyleDraftRequest struct {
	ExpectedRevision         int    `json:"expected_revision"`
	Prompt                   string `json:"prompt,omitempty"`
	TemplateID               string `json:"template_id,omitempty"`
	ReferenceAssetID         string `json:"reference_asset_id,omitempty"`
	ReferenceRightsConfirmed bool   `json:"reference_rights_confirmed"`
}

// EditorStyleDraft is a persisted, reviewable proposal. It remains separate
// from EditorSession.EditPlan until the caller explicitly applies it.
type EditorStyleDraft struct {
	SchemaVersion          string                        `json:"schema_version"`
	DraftID                string                        `json:"draft_id"`
	SessionID              string                        `json:"session_id"`
	BaseRevision           int                           `json:"base_revision"`
	CreatedAt              time.Time                     `json:"created_at"`
	Prompt                 string                        `json:"prompt,omitempty"`
	Template               VideoStyleTemplate            `json:"template"`
	StyleProfile           VideoStyleProfile             `json:"style_profile"`
	ProposedEditPlan       DemoEditPlan                  `json:"proposed_edit_plan"`
	ProposedPreviewProfile EditorRenderProfile           `json:"proposed_preview_profile"`
	ProposedFinalProfile   EditorRenderProfile           `json:"proposed_final_profile"`
	Validation             *DemoEditPlanValidationReport `json:"validation,omitempty"`
	RequiresConfirmation   bool                          `json:"requires_confirmation"`
	Warnings               []VideoStyleDraftWarning      `json:"warnings,omitempty"`
}

type VideoStyleDraftWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type EditorApplyStyleDraftRequest struct {
	ExpectedRevision int `json:"expected_revision"`
}
