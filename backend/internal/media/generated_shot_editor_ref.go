package media

import (
	"errors"
	"strings"
)

const (
	GeneratedShotEditorAssetRefSchemaVersion = "demoops.generated_shot_editor_asset_ref.v1"
	GeneratedShotEditorAssetKind             = "generated_video_candidate"
	GeneratedShotSourceMaterialPolicy        = "non_authoritative_generated_candidate"
)

// GeneratedShotEditorAssetRef is the only provider-neutral media reference
// emitted by this compiler. It contains a local normalized file and audit
// metadata, never a vendor request, response, model ID, or temporary URL.
// This is a patch-construction input, not an EditorSession mutation.
type GeneratedShotEditorAssetRef struct {
	SchemaVersion        string  `json:"schema_version"`
	AssetRefID           string  `json:"asset_ref_id"`
	IntentID             string  `json:"intent_id"`
	CandidateID          string  `json:"candidate_id"`
	Provider             string  `json:"provider"`
	Kind                 string  `json:"kind"`
	URI                  string  `json:"uri"`
	MimeType             string  `json:"mime_type"`
	SHA256               string  `json:"sha256"`
	SizeBytes            int64   `json:"size_bytes"`
	DurationSec          float64 `json:"duration_sec"`
	Width                int     `json:"width"`
	Height               int     `json:"height"`
	FPS                  float64 `json:"fps"`
	CFR                  bool    `json:"cfr"`
	NormalizationProfile string  `json:"normalization_profile"`
	SourceMaterialPolicy string  `json:"source_material_policy"`
	NonAuthoritative     bool    `json:"non_authoritative"`
	PresentationOnly     bool    `json:"presentation_only"`
	EditorApprovalID     string  `json:"editor_approval_id"`
	TargetPlanID         string  `json:"target_plan_id"`
	ExpectedPlanRevision int     `json:"expected_plan_revision"`
	Placement            string  `json:"placement"`
	ApprovedForDemo      bool    `json:"approved_for_demo"`
	IncludeInDemo        bool    `json:"include_in_demo"`
	AutoApply            bool    `json:"auto_apply"`
}

type GeneratedShotEditorAssetRefValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *GeneratedShotEditorAssetRefValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Field + ": " + e.Message
}

// CompileGeneratedShotEditorAssetRef performs the final provider-neutral
// reference compilation. It cannot call a provider, write an editor session,
// construct/apply a patch, or authorize rendering.
func CompileGeneratedShotEditorAssetRef(intent GeneratedShotIntent, candidate GeneratedShotCandidate, set GeneratedShotCandidateSet, selection GeneratedShotSelection, approval GeneratedShotEditorApproval) (GeneratedShotEditorAssetRef, error) {
	fail := func(field string, code string, message string) (GeneratedShotEditorAssetRef, error) {
		return GeneratedShotEditorAssetRef{}, &GeneratedShotEditorAssetRefValidationError{Field: field, Code: code, Message: message}
	}
	if err := ValidateGeneratedShotIntent(intent); err != nil {
		return fail("intent", "generated_editor_ref_intent_invalid", err.Error())
	}
	if err := ValidateGeneratedShotCandidate(candidate); err != nil {
		return fail("candidate", "generated_editor_ref_candidate_invalid", err.Error())
	}
	if err := ValidateGeneratedShotEditorApproval(set, selection, approval); err != nil {
		return fail("editor_approval", "generated_editor_ref_approval_invalid", err.Error())
	}
	if intent.IntentID != candidate.IntentID || intent.IntentID != set.IntentID || selection.IntentID != intent.IntentID || approval.IntentID != intent.IntentID {
		return fail("intent_id", "generated_editor_ref_binding_mismatch", "intent, candidate, set, selection, and approval must match")
	}
	if selection.SelectedCandidateID != candidate.CandidateID || approval.CandidateID != candidate.CandidateID || approval.Provider != candidate.Provider {
		return fail("candidate_id", "generated_editor_ref_candidate_mismatch", "selected candidate and approved candidate must match")
	}
	if approval.NormalizedSHA256 != strings.ToLower(candidate.NormalizedArtifact.SHA256) {
		return fail("sha256", "generated_editor_ref_digest_mismatch", "approval digest must match the normalized candidate")
	}
	if approval.TargetPlanID == "" || approval.ExpectedPlanRevision < 1 || approval.Placement == "" {
		return fail("target_plan_id", "generated_editor_ref_plan_binding_missing", "target plan, expected revision, and placement are required")
	}
	ref := GeneratedShotEditorAssetRef{
		SchemaVersion: GeneratedShotEditorAssetRefSchemaVersion,
		AssetRefID:    "generated_candidate_" + candidate.CandidateID + "_normalized",
		IntentID:      candidate.IntentID, CandidateID: candidate.CandidateID, Provider: candidate.Provider,
		Kind: GeneratedShotEditorAssetKind, URI: candidate.NormalizedArtifact.Path,
		MimeType: candidate.NormalizedArtifact.MimeType, SHA256: strings.ToLower(candidate.NormalizedArtifact.SHA256),
		SizeBytes: candidate.NormalizedArtifact.SizeBytes, DurationSec: candidate.NormalizedArtifact.Probe.DurationSec,
		Width: candidate.NormalizedArtifact.Probe.Width, Height: candidate.NormalizedArtifact.Probe.Height,
		FPS: candidate.NormalizedArtifact.Probe.FPS, CFR: candidate.NormalizedArtifact.Probe.CFR,
		NormalizationProfile: candidate.NormalizedArtifact.NormalizationProfile,
		SourceMaterialPolicy: GeneratedShotSourceMaterialPolicy,
		NonAuthoritative:     true, PresentationOnly: true, EditorApprovalID: approval.ApprovalID,
		TargetPlanID: approval.TargetPlanID, ExpectedPlanRevision: approval.ExpectedPlanRevision,
		Placement: approval.Placement, ApprovedForDemo: true, IncludeInDemo: false, AutoApply: false,
	}
	if err := ValidateGeneratedShotEditorAssetRef(ref); err != nil {
		return GeneratedShotEditorAssetRef{}, err
	}
	return ref, nil
}

func ValidateGeneratedShotEditorAssetRef(ref GeneratedShotEditorAssetRef) error {
	fail := func(field string, code string, message string) error {
		return &GeneratedShotEditorAssetRefValidationError{Field: field, Code: code, Message: message}
	}
	if ref.SchemaVersion != GeneratedShotEditorAssetRefSchemaVersion || strings.TrimSpace(ref.AssetRefID) == "" {
		return fail("schema_version", "generated_editor_ref_schema_unsupported", "editor asset reference schema and identity are required")
	}
	if strings.TrimSpace(ref.IntentID) == "" || strings.TrimSpace(ref.CandidateID) == "" || strings.TrimSpace(ref.Provider) == "" {
		return fail("candidate_id", "generated_editor_ref_identity_missing", "intent, candidate, and provider identities are required")
	}
	if ref.Kind != GeneratedShotEditorAssetKind {
		return fail("kind", "generated_editor_ref_kind_unsupported", "only generated_video_candidate references are supported")
	}
	if strings.TrimSpace(ref.URI) == "" || !strings.HasPrefix(ref.URI, "\\") && !(len(ref.URI) >= 3 && ref.URI[1] == ':' && (ref.URI[2] == '\\' || ref.URI[2] == '/')) || strings.Contains(ref.URI, "://") {
		return fail("uri", "generated_editor_ref_not_local", "editor reference must be an absolute local path, not a provider URL")
	}
	if strings.ToLower(ref.MimeType) != "video/mp4" || ref.SHA256 == "" || ref.SizeBytes <= 0 {
		return fail("media", "generated_editor_ref_media_invalid", "editor reference must contain local MP4 media with digest and positive size")
	}
	if ref.NormalizationProfile != GeneratedShotNormalizationProfile || ref.Width != GeneratedShotNormalizedWidth || ref.Height != GeneratedShotNormalizedHeight || ref.FPS < 29.99 || ref.FPS > 30.01 || !ref.CFR || ref.DurationSec <= 0 {
		return fail("normalization_profile", "generated_editor_ref_media_profile_invalid", "editor reference must use the locked MP4/H.264/yuv420p/1920x1080/CFR30 profile")
	}
	if ref.SourceMaterialPolicy != GeneratedShotSourceMaterialPolicy || !ref.NonAuthoritative || !ref.PresentationOnly {
		return fail("source_material_policy", "generated_editor_ref_authority_invalid", "editor reference must remain non-authoritative presentation material")
	}
	if strings.TrimSpace(ref.EditorApprovalID) == "" || strings.TrimSpace(ref.TargetPlanID) == "" || ref.ExpectedPlanRevision < 1 || strings.TrimSpace(ref.Placement) == "" {
		return fail("editor_approval_id", "generated_editor_ref_approval_binding_missing", "editor approval, target plan, revision, and placement are required")
	}
	if !ref.ApprovedForDemo || ref.IncludeInDemo || ref.AutoApply {
		return fail("approved_for_demo", "generated_editor_ref_safety_envelope_invalid", "reference may be approved for patch construction but cannot auto-include or auto-apply")
	}
	return nil
}

func asGeneratedShotEditorAssetRefValidationError(err error) *GeneratedShotEditorAssetRefValidationError {
	var target *GeneratedShotEditorAssetRefValidationError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
