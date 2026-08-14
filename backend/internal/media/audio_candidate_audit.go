package media

import (
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
)

const AudioCandidateAuditSchemaVersion = "cascade.audio_candidate_audit.v1"

// AudioCandidateAudit is the non-sensitive evidence retained for a generated
// audio candidate. Provider success alone never authorizes timeline adoption.
type AudioCandidateAudit struct {
	SchemaVersion            string               `json:"schema_version"`
	CreatedAt                time.Time            `json:"created_at"`
	Provider                 config.ModelProvider `json:"provider"`
	Model                    string               `json:"model"`
	Operation                AudioModelOperation  `json:"operation"`
	Mode                     config.ArkMediaMode  `json:"mode"`
	RealCallMade             bool                 `json:"real_call_made"`
	ProviderHTTPStatus       int                  `json:"provider_http_status,omitempty"`
	ErrorClass               string               `json:"error_class,omitempty"`
	TaskID                   string               `json:"task_id,omitempty"`
	ProviderStatus           string               `json:"provider_status,omitempty"`
	CandidatePresent         bool                 `json:"candidate_present"`
	CandidateAssetRef        string               `json:"candidate_asset_ref,omitempty"`
	CandidateSHA256          string               `json:"candidate_sha256,omitempty"`
	MimeType                 string               `json:"mime_type,omitempty"`
	DurationMS               int                  `json:"duration_ms,omitempty"`
	SampleRateHZ             int                  `json:"sample_rate_hz,omitempty"`
	Channels                 int                  `json:"channels,omitempty"`
	DownloadVerified         bool                 `json:"download_verified"`
	FFprobeVerified          bool                 `json:"ffprobe_verified"`
	ReviewState              string               `json:"review_state"`
	RequiresExplicitApproval bool                 `json:"requires_explicit_approval"`
	ProviderOutputAdopted    bool                 `json:"provider_output_adopted"`
	InsertedIntoDemoEditPlan bool                 `json:"inserted_into_demo_edit_plan"`
	LocalPathRecorded        bool                 `json:"local_path_recorded"`
	TranscriptRecorded       bool                 `json:"transcript_recorded"`
}

func NewAudioCandidateAudit(result AudioModelResult, createdAt time.Time) AudioCandidateAudit {
	audit := AudioCandidateAudit{
		SchemaVersion: AudioCandidateAuditSchemaVersion, CreatedAt: createdAt.UTC(), Provider: result.Provider,
		Model: result.Model, Operation: result.Operation, Mode: result.Mode,
		ProviderHTTPStatus: result.Trace.HTTPStatus, ErrorClass: result.Trace.ErrorClass,
		ReviewState: "candidate_only", RequiresExplicitApproval: true,
		ProviderOutputAdopted: false, InsertedIntoDemoEditPlan: false,
		LocalPathRecorded: false, TranscriptRecorded: false,
	}
	if result.Response != nil {
		audit.TaskID = result.Response.TaskID
		audit.ProviderStatus = result.Response.Status
	}
	audit.RealCallMade = result.Mode == config.ArkMediaModeReal && (result.Trace.HTTPStatus != 0 || strings.TrimSpace(audit.TaskID) != "")
	if result.Response == nil || result.Response.Output == nil {
		return audit
	}
	output := result.Response.Output
	audit.CandidatePresent = strings.TrimSpace(output.AssetRef) != "" && strings.TrimSpace(output.SHA256) != ""
	audit.CandidateAssetRef = output.AssetRef
	audit.CandidateSHA256 = strings.ToLower(strings.TrimSpace(output.SHA256))
	audit.MimeType = output.MimeType
	audit.DurationMS = output.DurationMS
	audit.SampleRateHZ = output.SampleRateHZ
	audit.Channels = output.Channels
	audit.DownloadVerified = audit.CandidatePresent && strings.TrimSpace(output.LocalPath) != ""
	audit.FFprobeVerified = audit.DownloadVerified && output.DurationMS > 0 && output.SampleRateHZ > 0 && output.Channels > 0
	return audit
}
