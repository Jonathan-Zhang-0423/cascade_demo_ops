package model

import (
	"errors"
	"time"
)

const ReplayManifestSchemaVersion = "demoops.replay_manifest.v1"

// ReplayManifest is a structured index generated at the end of every Server
// Browser Agent execution. It associates the original execution package with
// the full audit trail so a developer can recover the exact failure scene from
// local artifacts without replaying the run — no credentials required.
//
// The manifest does not duplicate artifact content; it references artifact IDs
// and file URIs already present in RecordingResultPackage.GeneratedAssets and
// StageEventLogRef. The Stages slice gives a lightweight, ordered summary of
// each stage's outcome, observed URL and failure code for quick triage.
type ReplayManifest struct {
	SchemaVersion string    `json:"schema_version"`
	ManifestID    string    `json:"manifest_id"`
	CreatedAt     time.Time `json:"created_at"`

	// Execution identity
	RunID            string `json:"run_id"`
	PackageID        string `json:"package_id"`
	BundleHashSHA256 string `json:"bundle_hash_sha256"`
	PolicyHashSHA256 string `json:"policy_hash_sha256"`

	// Dev-only context (populated when run under a test waiver)
	DevTestOnly          bool     `json:"dev_test_only,omitempty"`
	WaiverID             string   `json:"waiver_id,omitempty"`
	WaiverAllowedNodeIDs []string `json:"waiver_allowed_node_ids,omitempty"`
	WaiverBlockedReasons []string `json:"waiver_blocked_reasons,omitempty"`

	// Outcome summary
	Status        string             `json:"status"` // "success" | "failed"
	FailedNodeID  string             `json:"failed_node_id,omitempty"`
	FinalDecision ValidationDecision `json:"final_decision"`

	// Per-stage summary (ordered)
	Stages []ReplayManifestStage `json:"stages,omitempty"`

	// Validation report index (one entry per phase, ordered pre→runtime→post)
	ValidationReports []ReplayManifestValidationRef `json:"validation_reports,omitempty"`

	// Artifact references for key evidence
	MP4URI           string `json:"mp4_uri,omitempty"`
	RawRecordingURI  string `json:"raw_recording_uri,omitempty"`
	BrowserTraceURI  string `json:"browser_trace_uri,omitempty"`
	StageEventLogURI string `json:"stage_event_log_uri,omitempty"`
	ManifestURI      string `json:"manifest_uri,omitempty"`

	// Runtime versions for environment reproducibility
	ServerRuntimeVersion    string `json:"server_runtime_version,omitempty"`
	BrowserRuntimeVersion   string `json:"browser_runtime_version,omitempty"`
	VideoWorkerVersion      string `json:"video_worker_version,omitempty"`
	ProtocolRuntime         string `json:"protocol_runtime,omitempty"`
	ExecutionBundleRuntime  string `json:"execution_bundle_runtime,omitempty"`
}

// ReplayManifestStage is a lightweight summary of a single stage outcome.
type ReplayManifestStage struct {
	NodeID             string             `json:"node_id"`
	StageID            string             `json:"stage_id"`
	Order              int                `json:"order"`
	Status             string             `json:"status"` // "passed" | "failed" | "not_started"
	Waived             bool               `json:"waived,omitempty"`
	ValidationDecision ValidationDecision `json:"validation_decision,omitempty"`
	ObservedURL        string             `json:"observed_url,omitempty"`
	ObservedTitle      string             `json:"observed_title,omitempty"`
	FailureCode        string             `json:"failure_code,omitempty"`
	FailureDomain      ValidationCheckDomain `json:"failure_domain,omitempty"`
	EvidenceArtifactIDs []string          `json:"evidence_artifact_ids,omitempty"`
}

// ReplayManifestValidationRef points to one validation report within the
// RecordingResultPackage.ValidationReports slice.
type ReplayManifestValidationRef struct {
	ReportID  string          `json:"report_id"`
	Phase     ValidationPhase `json:"phase"`
	Decision  ValidationDecision `json:"decision"`
	CheckCount int             `json:"check_count"`
	FailCount  int             `json:"fail_count"`
}

// Validate returns an error if the manifest is missing required identity or
// timestamp fields.
func (m ReplayManifest) Validate() error {
	if m.ManifestID == "" || m.RunID == "" || m.PackageID == "" {
		return errors.New("replay manifest requires manifest_id, run_id and package_id")
	}
	if m.BundleHashSHA256 == "" {
		return errors.New("replay manifest requires bundle_hash_sha256")
	}
	if m.PolicyHashSHA256 == "" {
		return errors.New("replay manifest requires policy_hash_sha256")
	}
	if m.CreatedAt.IsZero() {
		return errors.New("replay manifest requires created_at")
	}
	return nil
}
