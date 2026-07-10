package model

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const ExchangeEnvelopeSchemaVersion = "demoops.exchange_envelope.v1"
const ClientExecutionPackageSchemaVersion = "demoops.client_execution_package.v1"
const RecordingResultPackageSchemaVersion = "demoops.recording_result_package.v1"
const ScriptFailureDiagnosticSchemaVersion = "demoops.script_failure_diagnostic.v1"

const CryptoSuiteXChaCha20Poly1305 = "xchacha20-poly1305"
const CryptoSuiteAES256GCM = "aes-256-gcm"
const KeyWrappingModeServerKMS = "server_kms"
const KeyWrappingModeServerPublicKey = "server_public_key"
const KeyWrappingModeCustomerKMS = "customer_kms"
const CompressionGzip = "gzip"
const CompressionNone = "none"
const PayloadRefKindInline = "inline"
const PayloadRefKindArtifact = "artifact"
const ResultRecipientAppInstallation = "app_installation"
const ResultRecipientOrganization = "organization"

type ExchangePackageKind string

const (
	ExchangePackageKindClientExecution ExchangePackageKind = "client_execution"
	ExchangePackageKindRecordingResult ExchangePackageKind = "recording_result"
)

type ExchangePackageStatus string

const (
	ExchangePackageStatusInitialized ExchangePackageStatus = "initialized"
	ExchangePackageStatusUploaded    ExchangePackageStatus = "uploaded"
	ExchangePackageStatusAccepted    ExchangePackageStatus = "accepted"
	ExchangePackageStatusQueued      ExchangePackageStatus = "queued"
	ExchangePackageStatusRunning     ExchangePackageStatus = "running"
	ExchangePackageStatusCompleted   ExchangePackageStatus = "completed"
	ExchangePackageStatusFailed      ExchangePackageStatus = "failed"
	ExchangePackageStatusCanceled    ExchangePackageStatus = "canceled"
	ExchangePackageStatusExpired     ExchangePackageStatus = "expired"
)

type RecordingJobStatus string

const (
	RecordingJobStatusQueued    RecordingJobStatus = "queued"
	RecordingJobStatusRunning   RecordingJobStatus = "running"
	RecordingJobStatusSucceeded RecordingJobStatus = "succeeded"
	RecordingJobStatusFailed    RecordingJobStatus = "failed"
	RecordingJobStatusCanceled  RecordingJobStatus = "canceled"
)

type RecordingResultStatus string

const (
	RecordingResultStatusGenerated RecordingResultStatus = "generated"
	RecordingResultStatusDelivered RecordingResultStatus = "delivered"
	RecordingResultStatusAcked     RecordingResultStatus = "acked"
	RecordingResultStatusFailed    RecordingResultStatus = "failed"
)

type ExchangeEnvelope struct {
	EnvelopeID           string                `json:"envelope_id"`
	OrgID                string                `json:"org_id"`
	ProjectID            string                `json:"project_id"`
	PackageKind          ExchangePackageKind   `json:"package_kind"`
	SchemaVersion        string                `json:"schema_version"`
	PayloadSchemaVersion string                `json:"payload_schema_version"`
	IdempotencyKey       string                `json:"idempotency_key"`
	CreatedAt            time.Time             `json:"created_at"`
	ExpiresAt            time.Time             `json:"expires_at"`
	Producer             ExchangeProducer      `json:"producer"`
	Crypto               ExchangeCrypto        `json:"crypto"`
	PayloadRef           EncryptedPayloadRef   `json:"payload_ref"`
	Attachments          []ExchangeAttachment  `json:"attachments,omitempty"`
	Policy               ExchangePackagePolicy `json:"policy"`
}

type ExchangeProducer struct {
	AppVersion     string `json:"app_version,omitempty"`
	InstallID      string `json:"install_id,omitempty"`
	DeviceID       string `json:"device_id,omitempty"`
	OS             string `json:"os,omitempty"`
	Arch           string `json:"arch,omitempty"`
	RuntimeProfile string `json:"runtime_profile,omitempty"`
}

type ExchangeCrypto struct {
	CryptoSuite            string `json:"crypto_suite,omitempty"`
	KeyWrappingMode        string `json:"key_wrapping_mode,omitempty"`
	ServerKeyID            string `json:"server_key_id"`
	KeyEncryptionAlg       string `json:"key_encryption_alg,omitempty"`
	ContentEncryptionAlg   string `json:"content_encryption_alg"`
	CompressionAlg         string `json:"compression_alg,omitempty"`
	PayloadDigestAlg       string `json:"payload_digest_alg,omitempty"`
	PayloadDigestSHA256    string `json:"payload_digest_sha256"`
	CiphertextDigestSHA256 string `json:"ciphertext_digest_sha256,omitempty"`
	SignatureAlg           string `json:"signature_alg"`
	SignatureKeyID         string `json:"signature_key_id"`
	Signature              string `json:"signature"`
	Nonce                  string `json:"nonce"`
	EncryptedContentKey    string `json:"encrypted_content_key,omitempty"`
	ContentKeyRef          string `json:"content_key_ref,omitempty"`
	KMSKeyRef              string `json:"kms_key_ref,omitempty"`
}

type EncryptedPayloadRef struct {
	Kind             string `json:"kind"`
	InlineCiphertext string `json:"inline_ciphertext,omitempty"`
	ArtifactID       string `json:"artifact_id,omitempty"`
	URI              string `json:"uri,omitempty"`
	MimeType         string `json:"mime_type,omitempty"`
	SHA256           string `json:"sha256,omitempty"`
	SizeBytes        int64  `json:"size_bytes,omitempty"`
	Encrypted        bool   `json:"encrypted,omitempty"`
	Sensitive        bool   `json:"sensitive,omitempty"`
	CompressionAlg   string `json:"compression_alg,omitempty"`
}

type ExchangeAttachment struct {
	ID             string         `json:"id"`
	Role           string         `json:"role"`
	Kind           string         `json:"kind"`
	URI            string         `json:"uri"`
	MimeType       string         `json:"mime_type,omitempty"`
	SHA256         string         `json:"sha256"`
	SizeBytes      int64          `json:"size_bytes,omitempty"`
	Encrypted      bool           `json:"encrypted"`
	Sensitive      bool           `json:"sensitive,omitempty"`
	CompressionAlg string         `json:"compression_alg,omitempty"`
	RecipientKeyID string         `json:"recipient_key_id,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type ExchangePackagePolicy struct {
	Retention              RetentionSpec `json:"retention"`
	DataResidency          string        `json:"data_residency,omitempty"`
	ReplayProtection       bool          `json:"replay_protection"`
	MaxExecutionWindowSec  int           `json:"max_execution_window_sec,omitempty"`
	DeletePayloadAfterRun  bool          `json:"delete_payload_after_run"`
	AllowDeltaPackage      bool          `json:"allow_delta_package,omitempty"`
	HumanApprovalRequired  bool          `json:"human_approval_required"`
	StructureSummaryOnly   bool          `json:"structure_summary_only"`
	RequiredIPAllowlistAck bool          `json:"required_ip_allowlist_ack"`
}

type ClientExecutionPackage struct {
	PackageID              string                           `json:"package_id"`
	OrgID                  string                           `json:"org_id"`
	ProjectID              string                           `json:"project_id"`
	SchemaVersion          string                           `json:"schema_version"`
	CreatedAt              time.Time                        `json:"created_at"`
	ApprovedAt             time.Time                        `json:"approved_at"`
	ProjectContextSummary  ProjectContextSummary            `json:"project_context_summary"`
	ProductMapSummary      ProductMapSummary                `json:"product_map_summary"`
	WorkflowGraph          *DemoWorkflowGraph               `json:"workflow_graph"`
	RecordingRunSpec       RecordingRunSpec                 `json:"recording_run_spec"`
	ExecutableScriptBundle *ExecutableRecordingScriptBundle `json:"executable_script_bundle,omitempty"`
	CredentialGrants       []CredentialGrant                `json:"credential_grants,omitempty"`
	EvidenceBundle         EvidenceBundle                   `json:"evidence_bundle"`
	Reproducibility        ReproducibilitySpec              `json:"reproducibility"`
	SafetyReport           PackageSafetyReport              `json:"safety_report"`
	RepairContext          *ScriptRepairContext             `json:"repair_context,omitempty"`
	Metadata               map[string]any                   `json:"metadata,omitempty"`
}

type ProjectContextSummary struct {
	ContextID         string            `json:"context_id"`
	SchemaVersion     string            `json:"schema_version,omitempty"`
	Mode              AppMode           `json:"mode"`
	Name              string            `json:"name,omitempty"`
	ProductURL        string            `json:"product_url"`
	TargetAudience    string            `json:"target_audience"`
	UseCases          []DemoUseCase     `json:"use_cases,omitempty"`
	Goals             []DemoGoal        `json:"goals,omitempty"`
	Audiences         []AudienceProfile `json:"audiences,omitempty"`
	BrandKit          *BrandKit         `json:"brand_kit,omitempty"`
	AccessPolicy      *AccessPolicy     `json:"access_policy,omitempty"`
	SecurityPolicy    *SecurityPolicy   `json:"security_policy,omitempty"`
	InputFingerprints map[string]string `json:"input_fingerprints,omitempty"`
	KnowledgeRefs     []EvidenceRef     `json:"knowledge_refs,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

type ProductMapSummary struct {
	ProductMapID string               `json:"product_map_id,omitempty"`
	Version      int                  `json:"version,omitempty"`
	Summary      string               `json:"summary,omitempty"`
	Pages        []*ProductPage       `json:"pages,omitempty"`
	Features     []*Feature           `json:"features,omitempty"`
	Routes       []*RouteMapNode      `json:"routes,omitempty"`
	Components   []ComponentSummary   `json:"components,omitempty"`
	DataModels   []DataModelSummary   `json:"data_models,omitempty"`
	Roles        []*ProductRole       `json:"roles,omitempty"`
	Workflows    []*WorkflowCandidate `json:"workflows,omitempty"`
	EvidenceRefs []EvidenceRef        `json:"evidence_refs,omitempty"`
}

type ComponentSummary struct {
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	Kind               string        `json:"kind,omitempty"`
	FilePathHashSHA256 string        `json:"file_path_hash_sha256,omitempty"`
	SelectorCount      int           `json:"selector_count,omitempty"`
	ActionCount        int           `json:"action_count,omitempty"`
	FeatureRefs        []string      `json:"feature_refs,omitempty"`
	EvidenceRefs       []EvidenceRef `json:"evidence_refs,omitempty"`
}

type DataModelSummary struct {
	ID                   string        `json:"id"`
	Name                 string        `json:"name"`
	Kind                 string        `json:"kind,omitempty"`
	Fields               []DataField   `json:"fields,omitempty"`
	SourcePathHashSHA256 string        `json:"source_path_hash_sha256,omitempty"`
	EvidenceRefs         []EvidenceRef `json:"evidence_refs,omitempty"`
}

type RecordingRunSpec struct {
	RunID               string                 `json:"run_id,omitempty"`
	BaseURL             string                 `json:"base_url"`
	AllowedDomains      []string               `json:"allowed_domains"`
	RequiredIPAllowlist []string               `json:"required_ip_allowlist,omitempty"`
	AuthFlowRef         string                 `json:"auth_flow_ref,omitempty"`
	Timezone            string                 `json:"timezone,omitempty"`
	Locale              string                 `json:"locale,omitempty"`
	Browser             BrowserRunSpec         `json:"browser"`
	Timeline            RecordingTimeline      `json:"timeline"`
	Outputs             RecordingOutputRequest `json:"outputs"`
	Redactions          RedactionPolicy        `json:"redactions"`
	FailurePolicy       RecordingFailurePolicy `json:"failure_policy"`
	Environment         map[string]string      `json:"environment,omitempty"`
}

type BrowserRunSpec struct {
	Engine        string         `json:"engine"`
	VersionPolicy string         `json:"version_policy,omitempty"`
	Headless      bool           `json:"headless"`
	Viewports     []ViewportSpec `json:"viewports,omitempty"`
}

type RecordingTimeline struct {
	TargetDurationSec int              `json:"target_duration_sec"`
	MaxDurationSec    int              `json:"max_duration_sec,omitempty"`
	CaptureWindows    []CaptureWindow  `json:"capture_windows,omitempty"`
	NodeTimingHints   []NodeTimingHint `json:"node_timing_hints,omitempty"`
}

type CaptureWindow struct {
	ID         string `json:"id"`
	NodeID     string `json:"node_id,omitempty"`
	StartMS    int    `json:"start_ms"`
	DurationMS int    `json:"duration_ms"`
	Role       string `json:"role,omitempty"`
}

type NodeTimingHint struct {
	NodeID      string `json:"node_id"`
	DurationMS  int    `json:"duration_ms,omitempty"`
	HoldAfterMS int    `json:"hold_after_ms,omitempty"`
}

type RecordingOutputRequest struct {
	RawRecording     bool     `json:"raw_recording"`
	FinalVideo       bool     `json:"final_video"`
	ScreenshotPack   bool     `json:"screenshot_pack"`
	StepByStepDocs   bool     `json:"step_by_step_docs"`
	Trace            bool     `json:"trace"`
	OutputFormats    []string `json:"output_formats,omitempty"`
	ResolutionWidth  int      `json:"resolution_width,omitempty"`
	ResolutionHeight int      `json:"resolution_height,omitempty"`
}

type RedactionPolicy struct {
	MaskSelectors        []string        `json:"mask_selectors,omitempty"`
	TextPatterns         []string        `json:"text_patterns,omitempty"`
	VideoMaskPolicy      string          `json:"video_mask_policy,omitempty"`
	ScreenshotMaskPolicy string          `json:"screenshot_mask_policy,omitempty"`
	Redactions           []RedactionSpec `json:"redactions,omitempty"`
}

type RecordingFailurePolicy struct {
	RetryAttempts             int      `json:"retry_attempts,omitempty"`
	SelectorRepairAllowed     bool     `json:"selector_repair_allowed"`
	DataRepairAllowed         bool     `json:"data_repair_allowed"`
	MaxRepairAttempts         int      `json:"max_repair_attempts,omitempty"`
	HumanEscalationConditions []string `json:"human_escalation_conditions,omitempty"`
}

type CredentialGrant struct {
	GrantID                     string    `json:"grant_id"`
	Kind                        string    `json:"kind"`
	Purpose                     string    `json:"purpose"`
	Scope                       string    `json:"scope,omitempty"`
	ExpiresAt                   time.Time `json:"expires_at,omitempty"`
	CloudSecretRef              string    `json:"cloud_secret_ref,omitempty"`
	EncryptedSecretAttachmentID string    `json:"encrypted_secret_attachment_id,omitempty"`
	AllowedDomains              []string  `json:"allowed_domains,omitempty"`
	AllowedOperations           []string  `json:"allowed_operations,omitempty"`
	RotationRequiredAfterRun    bool      `json:"rotation_required_after_run"`
	DeleteAfterRun              bool      `json:"delete_after_run"`
}

type EvidenceBundle struct {
	EvidenceRefs        []EvidenceRef               `json:"evidence_refs,omitempty"`
	EvidenceSummaries   []EvidenceSummary           `json:"evidence_summaries,omitempty"`
	SourceTrees         []SourceTreeDigest          `json:"source_trees,omitempty"`
	RequirementDigests  []ContentDigest             `json:"requirement_digests,omitempty"`
	ScreenshotDigests   []ContentDigest             `json:"screenshot_digests,omitempty"`
	ArtifactDescriptors []PackageArtifactDescriptor `json:"artifact_descriptors,omitempty"`
}

type EvidenceSummary struct {
	ID           string        `json:"id"`
	Kind         EvidenceKind  `json:"kind"`
	Summary      string        `json:"summary,omitempty"`
	Confidence   float64       `json:"confidence,omitempty"`
	Sensitive    bool          `json:"sensitive,omitempty"`
	ArtifactRefs []ArtifactRef `json:"artifact_refs,omitempty"`
}

type SourceTreeDigest struct {
	RepositoryID     string       `json:"repository_id,omitempty"`
	Branch           string       `json:"branch,omitempty"`
	CommitSHA        string       `json:"commit_sha,omitempty"`
	RootDigestSHA256 string       `json:"root_digest_sha256"`
	FileCount        int          `json:"file_count,omitempty"`
	PathDigests      []PathDigest `json:"path_digests,omitempty"`
}

type PathDigest struct {
	PathHashSHA256 string `json:"path_hash_sha256"`
	ContentSHA256  string `json:"content_sha256,omitempty"`
	Kind           string `json:"kind,omitempty"`
	Language       string `json:"language,omitempty"`
	Entrypoint     bool   `json:"entrypoint,omitempty"`
}

type ContentDigest struct {
	ID        string `json:"id"`
	Kind      string `json:"kind,omitempty"`
	Title     string `json:"title,omitempty"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

type PackageArtifactDescriptor struct {
	ID             string         `json:"id"`
	Role           string         `json:"role,omitempty"`
	Kind           string         `json:"kind"`
	URI            string         `json:"uri"`
	MimeType       string         `json:"mime_type,omitempty"`
	SHA256         string         `json:"sha256"`
	SizeBytes      int64          `json:"size_bytes,omitempty"`
	Encrypted      bool           `json:"encrypted"`
	Sensitive      bool           `json:"sensitive,omitempty"`
	CompressionAlg string         `json:"compression_alg,omitempty"`
	RecipientKeyID string         `json:"recipient_key_id,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type ReproducibilitySpec struct {
	GraphHashSHA256       string            `json:"graph_hash_sha256"`
	PackageHashSHA256     string            `json:"package_hash_sha256,omitempty"`
	ScriptHashSHA256      string            `json:"script_hash_sha256,omitempty"`
	InputFingerprints     map[string]string `json:"input_fingerprints,omitempty"`
	BrowserRuntimePins    map[string]string `json:"browser_runtime_pins,omitempty"`
	SourceSnapshotDigest  string            `json:"source_snapshot_digest,omitempty"`
	DeterministicSeed     string            `json:"deterministic_seed,omitempty"`
	CreatedWithAppVersion string            `json:"created_with_app_version,omitempty"`
}

type PackageSafetyReport struct {
	AllowedToUpload    bool               `json:"allowed_to_upload"`
	UploadMode         string             `json:"upload_mode"`
	HumanApproval      UserApprovalRecord `json:"human_approval"`
	ForbiddenPages     []string           `json:"forbidden_pages,omitempty"`
	ForbiddenData      []string           `json:"forbidden_data,omitempty"`
	RedactionSelectors []string           `json:"redaction_selectors,omitempty"`
	PIIHandling        string             `json:"pii_handling,omitempty"`
	DataResidency      string             `json:"data_residency,omitempty"`
	PolicyFindings     []AgentFinding     `json:"policy_findings,omitempty"`
}

type UserApprovalRecord struct {
	ApprovalID       string    `json:"approval_id"`
	ApprovedByUserID string    `json:"approved_by_user_id,omitempty"`
	ApprovedAt       time.Time `json:"approved_at"`
	PlanDigestSHA256 string    `json:"plan_digest_sha256"`
	ReviewedNodeIDs  []string  `json:"reviewed_node_ids,omitempty"`
	Notes            []string  `json:"notes,omitempty"`
}

type RecordingResultPackage struct {
	ResultID              string                   `json:"result_id"`
	SourcePackageID       string                   `json:"source_package_id"`
	CloudJobID            string                   `json:"cloud_job_id"`
	SchemaVersion         string                   `json:"schema_version"`
	Status                RecordingResultStatus    `json:"status"`
	ExecutionTrace        *ExecutionTrace          `json:"execution_trace,omitempty"`
	StepResults           []StepResult             `json:"step_results,omitempty"`
	GeneratedAssets       []ArtifactRef            `json:"generated_assets,omitempty"`
	VerificationReport    VerificationReport       `json:"verification_report"`
	FailureDiagnostic     *ScriptFailureDiagnostic `json:"failure_diagnostic,omitempty"`
	RepairRequest         *ScriptRepairRequest     `json:"repair_request,omitempty"`
	GraphPatchSuggestions []GraphPatch             `json:"graph_patch_suggestions,omitempty"`
	AuditTrail            CloudExecutionAuditTrail `json:"audit_trail"`
	Delivery              ResultDelivery           `json:"delivery"`
	CreatedAt             time.Time                `json:"created_at"`
}

type ScriptFailureDiagnostic struct {
	ID                       string                      `json:"id"`
	SchemaVersion            string                      `json:"schema_version"`
	SourcePackageID          string                      `json:"source_package_id"`
	CloudJobID               string                      `json:"cloud_job_id"`
	FailedNodeID             string                      `json:"failed_node_id"`
	FailedStepOrder          int                         `json:"failed_step_order,omitempty"`
	Attempt                  int                         `json:"attempt,omitempty"`
	Error                    AgentError                  `json:"error"`
	CurrentURL               string                      `json:"current_url,omitempty"`
	PageTitle                string                      `json:"page_title,omitempty"`
	ScreenshotRefs           []PackageArtifactDescriptor `json:"screenshot_refs,omitempty"`
	TraceRefs                []PackageArtifactDescriptor `json:"trace_refs,omitempty"`
	ConsoleEvents            []ConsoleEventSummary       `json:"console_events,omitempty"`
	NetworkEvents            []NetworkEventSummary       `json:"network_events,omitempty"`
	DOMSnapshotRef           *PackageArtifactDescriptor  `json:"dom_snapshot_ref,omitempty"`
	AccessibilitySnapshotRef *PackageArtifactDescriptor  `json:"accessibility_snapshot_ref,omitempty"`
	RedactionReport          DiagnosticRedactionReport   `json:"redaction_report"`
	RepairHints              []ScriptRepairHint          `json:"repair_hints,omitempty"`
	CapturedAt               time.Time                   `json:"captured_at,omitempty"`
}

type ConsoleEventSummary struct {
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	URL       string    `json:"url,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

type NetworkEventSummary struct {
	URL       string    `json:"url"`
	Method    string    `json:"method,omitempty"`
	Status    int       `json:"status,omitempty"`
	Failure   string    `json:"failure,omitempty"`
	Resource  string    `json:"resource,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Redacted  bool      `json:"redacted,omitempty"`
}

type DiagnosticRedactionReport struct {
	Applied             bool           `json:"applied"`
	PolicyRef           string         `json:"policy_ref,omitempty"`
	MaskedSelectors     []string       `json:"masked_selectors,omitempty"`
	MaskedTextPatterns  []string       `json:"masked_text_patterns,omitempty"`
	StrippedHeaders     []string       `json:"stripped_headers,omitempty"`
	StrippedStorageKeys []string       `json:"stripped_storage_keys,omitempty"`
	FullHTMLIncluded    bool           `json:"full_html_included"`
	PolicyFindings      []AgentFinding `json:"policy_findings,omitempty"`
}

type ScriptRepairHint struct {
	Kind               string              `json:"kind"`
	Summary            string              `json:"summary"`
	NodeID             string              `json:"node_id,omitempty"`
	SelectorCandidates []SelectorCandidate `json:"selector_candidates,omitempty"`
	SuggestedAction    string              `json:"suggested_action,omitempty"`
	Confidence         float64             `json:"confidence,omitempty"`
	EvidenceRefs       []EvidenceRef       `json:"evidence_refs,omitempty"`
}

type ScriptRepairRequest struct {
	ID                     string    `json:"id"`
	SourceResultID         string    `json:"source_result_id"`
	SourcePackageID        string    `json:"source_package_id"`
	CloudJobID             string    `json:"cloud_job_id"`
	FailedBundleHashSHA256 string    `json:"failed_bundle_hash_sha256,omitempty"`
	FailedPlanHashSHA256   string    `json:"failed_plan_hash_sha256,omitempty"`
	MaxRepairAttempts      int       `json:"max_repair_attempts,omitempty"`
	RepairAttempt          int       `json:"repair_attempt,omitempty"`
	ApprovalRequired       bool      `json:"approval_required"`
	RequestedAt            time.Time `json:"requested_at,omitempty"`
	ExpiresAt              time.Time `json:"expires_at,omitempty"`
}

type ScriptRepairContext struct {
	SourceResultID       string                   `json:"source_result_id"`
	SourcePackageID      string                   `json:"source_package_id"`
	SourceCloudJobID     string                   `json:"source_cloud_job_id"`
	RepairAttempt        int                      `json:"repair_attempt"`
	BaseBundleID         string                   `json:"base_bundle_id,omitempty"`
	BaseBundleHashSHA256 string                   `json:"base_bundle_hash_sha256,omitempty"`
	BasePlanHashSHA256   string                   `json:"base_plan_hash_sha256,omitempty"`
	DiagnosticRefs       []EvidenceRef            `json:"diagnostic_refs,omitempty"`
	FailureDiagnostic    *ScriptFailureDiagnostic `json:"failure_diagnostic,omitempty"`
	UserApproval         *UserApprovalRecord      `json:"user_approval,omitempty"`
	IdempotencyKey       string                   `json:"idempotency_key,omitempty"`
}

type VerificationReport struct {
	PassRate             float64         `json:"pass_rate,omitempty"`
	FailedNodeIDs        []string        `json:"failed_node_ids,omitempty"`
	PolicyFindings       []AgentFinding  `json:"policy_findings,omitempty"`
	ReproducibilityMatch bool            `json:"reproducibility_match"`
	OutputChecksums      []ContentDigest `json:"output_checksums,omitempty"`
}

type CloudExecutionAuditTrail struct {
	CloudWorkerID       string            `json:"cloud_worker_id,omitempty"`
	StartedAt           time.Time         `json:"started_at,omitempty"`
	CompletedAt         time.Time         `json:"completed_at,omitempty"`
	RuntimeVersions     map[string]string `json:"runtime_versions,omitempty"`
	SourcePackageDigest string            `json:"source_package_digest,omitempty"`
	GraphDigest         string            `json:"graph_digest,omitempty"`
	ExecutionIP         string            `json:"execution_ip,omitempty"`
}

type ResultDelivery struct {
	ResultPackageRef PackageArtifactDescriptor   `json:"result_package_ref"`
	AssetRefs        []PackageArtifactDescriptor `json:"asset_refs,omitempty"`
	RecipientKind    string                      `json:"recipient_kind,omitempty"`
	RecipientKeyID   string                      `json:"recipient_key_id,omitempty"`
	EncryptionAlg    string                      `json:"encryption_alg,omitempty"`
	ExpiresAt        time.Time                   `json:"expires_at,omitempty"`
	AckRequired      bool                        `json:"ack_required"`
	AckedAt          time.Time                   `json:"acked_at,omitempty"`
}

func (r *RecordingResultPackage) ValidateStatusContract() error {
	if r == nil {
		return errors.New("recording result package is nil")
	}
	if r.SchemaVersion != RecordingResultPackageSchemaVersion {
		return errors.New("unsupported recording result package schema version")
	}
	if r.Status != RecordingResultStatusFailed {
		return nil
	}
	if r.FailureDiagnostic == nil {
		return errors.New("failed recording result requires failure_diagnostic")
	}
	if r.RepairRequest == nil {
		return errors.New("failed recording result requires repair_request")
	}
	if !r.RepairRequest.ApprovalRequired {
		return errors.New("script repair request must require approval")
	}
	if r.RepairRequest.SourceResultID != r.ResultID || r.RepairRequest.SourcePackageID != r.SourcePackageID || r.RepairRequest.CloudJobID != r.CloudJobID {
		return errors.New("script repair request does not match failed result identity")
	}
	return r.FailureDiagnostic.ValidateSafety()
}

func (d *ScriptFailureDiagnostic) ValidateSafety() error {
	if d == nil {
		return errors.New("script failure diagnostic is nil")
	}
	if d.SchemaVersion != ScriptFailureDiagnosticSchemaVersion {
		return errors.New("unsupported script failure diagnostic schema version")
	}
	if d.FailedNodeID == "" || d.Error.Code == "" {
		return errors.New("script failure diagnostic missing failed node or error")
	}
	if !d.RedactionReport.Applied || d.RedactionReport.FullHTMLIncluded {
		return errors.New("script failure diagnostic must be redacted and must not include full HTML")
	}
	for _, value := range diagnosticTextFields(d) {
		if containsUnsafeDiagnosticToken(value) {
			return errors.New("script failure diagnostic contains unsafe secret-like text")
		}
	}
	for _, artifact := range diagnosticArtifacts(d) {
		if !artifact.Sensitive || !artifact.Encrypted {
			return errors.New("script failure diagnostic artifacts must be sensitive and encrypted")
		}
	}
	return nil
}

func diagnosticTextFields(d *ScriptFailureDiagnostic) []string {
	values := []string{d.CurrentURL, d.PageTitle, d.Error.Code, d.Error.Message}
	for _, event := range d.ConsoleEvents {
		values = append(values, event.Message, event.URL)
	}
	for _, event := range d.NetworkEvents {
		values = append(values, event.URL, event.Failure)
	}
	for _, hint := range d.RepairHints {
		values = append(values, hint.Summary, hint.SuggestedAction)
	}
	return values
}

func diagnosticArtifacts(d *ScriptFailureDiagnostic) []PackageArtifactDescriptor {
	artifacts := append([]PackageArtifactDescriptor{}, d.ScreenshotRefs...)
	artifacts = append(artifacts, d.TraceRefs...)
	if d.DOMSnapshotRef != nil {
		artifacts = append(artifacts, *d.DOMSnapshotRef)
	}
	if d.AccessibilitySnapshotRef != nil {
		artifacts = append(artifacts, *d.AccessibilitySnapshotRef)
	}
	return artifacts
}

func containsUnsafeDiagnosticToken(value string) bool {
	lower := strings.ToLower(value)
	for _, token := range []string{"begin private key", "bearer ", "authorization:", "authorization=", "cookie:", "cookie=", "localstorage", "sessionstorage"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	if strings.Contains(value, "sk-") {
		return true
	}
	return false
}

type ExecutionPackageInitRequest struct {
	OrgID       string              `json:"org_id"`
	ProjectID   string              `json:"project_id"`
	PackageKind ExchangePackageKind `json:"package_kind"`
	Producer    ExchangeProducer    `json:"producer"`
}

type ExecutionPackageInitResponse struct {
	UploadID              string    `json:"upload_id"`
	ServerPublicKeyID     string    `json:"server_public_key_id"`
	ServerPublicKeyAlg    string    `json:"server_public_key_alg,omitempty"`
	KeyWrappingModes      []string  `json:"key_wrapping_modes,omitempty"`
	SupportedCryptoSuites []string  `json:"supported_crypto_suites,omitempty"`
	SupportedCompression  []string  `json:"supported_compression,omitempty"`
	CascadeExecutionIPs   []string  `json:"cascade_execution_ips"`
	MaxEnvelopeBytes      int64     `json:"max_envelope_bytes"`
	MaxAttachmentBytes    int64     `json:"max_attachment_bytes"`
	ExpiresAt             time.Time `json:"expires_at"`
}

type ExecutionPackageUploadRequest struct {
	UploadID string           `json:"upload_id"`
	Envelope ExchangeEnvelope `json:"envelope"`
}

type ExecutionPackageUploadResponse struct {
	ExchangePackageID string                `json:"exchange_package_id"`
	CloudJobID        string                `json:"cloud_job_id"`
	Status            ExchangePackageStatus `json:"status"`
}

type ExecutionPackageStatusResponse struct {
	ExchangePackageID string                `json:"exchange_package_id"`
	CloudJobID        string                `json:"cloud_job_id,omitempty"`
	Status            ExchangePackageStatus `json:"status"`
	ResultPackageID   string                `json:"result_package_id,omitempty"`
	Error             *AgentError           `json:"error,omitempty"`
	UpdatedAt         time.Time             `json:"updated_at"`
}

type ResultPackageAckRequest struct {
	ResultPackageID  string    `json:"result_package_id"`
	AckedByInstallID string    `json:"acked_by_install_id,omitempty"`
	AckedAt          time.Time `json:"acked_at"`
}

type ResultPackageAckResponse struct {
	ResultPackageID string                `json:"result_package_id"`
	Status          RecordingResultStatus `json:"status"`
	Retention       RetentionSpec         `json:"retention"`
}

type ExchangeSignatureVerifier interface {
	VerifyExchangeSignature(envelope *ExchangeEnvelope, canonicalPayload []byte) bool
}

func CanonicalJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}

func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func DigestCanonicalJSON(value any) (string, error) {
	data, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	return SHA256Hex(data), nil
}

type EncryptedPayloadPackage struct {
	PlaintextDigestSHA256  string `json:"plaintext_digest_sha256"`
	CiphertextDigestSHA256 string `json:"ciphertext_digest_sha256"`
	CompressedSizeBytes    int64  `json:"compressed_size_bytes"`
	CiphertextSizeBytes    int64  `json:"ciphertext_size_bytes"`
	NonceBase64            string `json:"nonce_base64"`
	CiphertextBase64       string `json:"ciphertext_base64"`
	CompressionAlg         string `json:"compression_alg"`
	CryptoSuite            string `json:"crypto_suite"`
}

func EncryptCanonicalJSONForExchange(value any, contentKey []byte, suite string, compressionAlg string) (EncryptedPayloadPackage, error) {
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return EncryptedPayloadPackage{}, err
	}
	plaintextDigest := SHA256Hex(canonical)
	compressed, err := compressPayload(canonical, compressionAlg)
	if err != nil {
		return EncryptedPayloadPackage{}, err
	}
	ciphertext, nonce, err := encryptPayloadBytes(compressed, contentKey, suite)
	if err != nil {
		return EncryptedPayloadPackage{}, err
	}
	return EncryptedPayloadPackage{
		PlaintextDigestSHA256:  plaintextDigest,
		CiphertextDigestSHA256: SHA256Hex(ciphertext),
		CompressedSizeBytes:    int64(len(compressed)),
		CiphertextSizeBytes:    int64(len(ciphertext)),
		NonceBase64:            base64.StdEncoding.EncodeToString(nonce),
		CiphertextBase64:       base64.StdEncoding.EncodeToString(ciphertext),
		CompressionAlg:         normalizeCompression(compressionAlg),
		CryptoSuite:            normalizeCryptoSuite(suite),
	}, nil
}

func DecryptCanonicalJSONFromExchange(pkg EncryptedPayloadPackage, contentKey []byte, target any) error {
	if target == nil {
		return errors.New("target is required")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(pkg.CiphertextBase64)
	if err != nil {
		return err
	}
	if pkg.CiphertextDigestSHA256 != "" && pkg.CiphertextDigestSHA256 != SHA256Hex(ciphertext) {
		return errors.New("encrypted payload ciphertext digest mismatch")
	}
	nonce, err := base64.StdEncoding.DecodeString(pkg.NonceBase64)
	if err != nil {
		return err
	}
	compressed, err := decryptPayloadBytes(ciphertext, nonce, contentKey, pkg.CryptoSuite)
	if err != nil {
		return err
	}
	plain, err := decompressPayload(compressed, pkg.CompressionAlg)
	if err != nil {
		return err
	}
	if pkg.PlaintextDigestSHA256 != "" && pkg.PlaintextDigestSHA256 != SHA256Hex(plain) {
		return errors.New("encrypted payload plaintext digest mismatch")
	}
	return json.Unmarshal(plain, target)
}

func NewRandomContentKey(suite string) ([]byte, error) {
	switch normalizeCryptoSuite(suite) {
	case CryptoSuiteAES256GCM:
		key := make([]byte, 32)
		_, err := rand.Read(key)
		return key, err
	case CryptoSuiteXChaCha20Poly1305:
		return nil, errors.New("xchacha20-poly1305 crypto backend is not linked in this build")
	default:
		return nil, fmt.Errorf("unsupported crypto_suite %q", suite)
	}
}

func compressPayload(data []byte, compressionAlg string) ([]byte, error) {
	switch normalizeCompression(compressionAlg) {
	case "", CompressionNone:
		out := make([]byte, len(data))
		copy(out, data)
		return out, nil
	case CompressionGzip:
		var buf bytes.Buffer
		writer := gzip.NewWriter(&buf)
		if _, err := writer.Write(data); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("unsupported compression_alg %q", compressionAlg)
	}
}

func decompressPayload(data []byte, compressionAlg string) ([]byte, error) {
	switch normalizeCompression(compressionAlg) {
	case "", CompressionNone:
		out := make([]byte, len(data))
		copy(out, data)
		return out, nil
	case CompressionGzip:
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	default:
		return nil, fmt.Errorf("unsupported compression_alg %q", compressionAlg)
	}
}

func encryptPayloadBytes(plain []byte, contentKey []byte, suite string) ([]byte, []byte, error) {
	normalizedSuite := normalizeCryptoSuite(suite)
	if normalizedSuite != CryptoSuiteAES256GCM {
		return nil, nil, fmt.Errorf("unsupported crypto_suite %q", suite)
	}
	if len(contentKey) != 32 {
		return nil, nil, errors.New("content key must be 32 bytes")
	}
	aead, err := newAEAD(contentKey, normalizedSuite)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return aead.Seal(nil, nonce, plain, nil), nonce, nil
}

func decryptPayloadBytes(ciphertext []byte, nonce []byte, contentKey []byte, suite string) ([]byte, error) {
	normalizedSuite := normalizeCryptoSuite(suite)
	if normalizedSuite != CryptoSuiteAES256GCM {
		return nil, fmt.Errorf("unsupported crypto_suite %q", suite)
	}
	if len(contentKey) != 32 {
		return nil, errors.New("content key must be 32 bytes")
	}
	aead, err := newAEAD(contentKey, normalizedSuite)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, errors.New("invalid nonce size")
	}
	return aead.Open(nil, nonce, ciphertext, nil)
}

func newAEAD(contentKey []byte, suite string) (cipher.AEAD, error) {
	block, err := aes.NewCipher(contentKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func normalizeCryptoSuite(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return CryptoSuiteAES256GCM
	}
	return value
}

func normalizeCompression(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return CompressionNone
	}
	return value
}

func (e *ExchangeEnvelope) ValidateForPayload(payload any, now time.Time, seenNonces map[string]bool, verifier ExchangeSignatureVerifier) error {
	canonicalPayload, err := CanonicalJSON(payload)
	if err != nil {
		return err
	}
	return e.ValidateForCanonicalPayload(canonicalPayload, now, seenNonces, verifier)
}

func (e *ExchangeEnvelope) ValidateForCanonicalPayload(canonicalPayload []byte, now time.Time, seenNonces map[string]bool, verifier ExchangeSignatureVerifier) error {
	if e == nil {
		return errors.New("exchange envelope is nil")
	}
	if e.SchemaVersion != ExchangeEnvelopeSchemaVersion {
		return errors.New("unsupported exchange envelope schema version")
	}
	if e.EnvelopeID == "" || e.OrgID == "" || e.ProjectID == "" || e.IdempotencyKey == "" {
		return errors.New("exchange envelope missing required identity fields")
	}
	if e.PackageKind == "" || e.PayloadSchemaVersion == "" {
		return errors.New("exchange envelope missing package kind or payload schema version")
	}
	if err := e.ValidateCryptoPolicy(); err != nil {
		return err
	}
	if !e.ExpiresAt.IsZero() && now.After(e.ExpiresAt) {
		return errors.New("exchange envelope is expired")
	}
	if e.Crypto.Nonce == "" {
		return errors.New("exchange envelope nonce is required")
	}
	if seenNonces != nil && seenNonces[e.Crypto.Nonce] {
		return errors.New("exchange envelope nonce has already been used")
	}
	if e.Crypto.PayloadDigestSHA256 == "" || e.Crypto.PayloadDigestSHA256 != SHA256Hex(canonicalPayload) {
		return errors.New("exchange envelope payload digest mismatch")
	}
	if e.Crypto.Signature == "" || e.Crypto.SignatureKeyID == "" || e.Crypto.SignatureAlg == "" {
		return errors.New("exchange envelope signature metadata is required")
	}
	if verifier != nil && !verifier.VerifyExchangeSignature(e, canonicalPayload) {
		return errors.New("exchange envelope signature verification failed")
	}
	if seenNonces != nil {
		seenNonces[e.Crypto.Nonce] = true
	}
	return nil
}

func (e *ExchangeEnvelope) ValidateCryptoPolicy() error {
	if e == nil {
		return errors.New("exchange envelope is nil")
	}
	crypto := e.Crypto
	suite := normalizeCryptoSuite(firstNonEmptyString(crypto.CryptoSuite, crypto.ContentEncryptionAlg))
	if suite != CryptoSuiteAES256GCM && suite != CryptoSuiteXChaCha20Poly1305 {
		return fmt.Errorf("unsupported crypto_suite %q", suite)
	}
	if crypto.CryptoSuite != "" && crypto.ContentEncryptionAlg != "" && crypto.CryptoSuite != crypto.ContentEncryptionAlg {
		return errors.New("crypto_suite and content_encryption_alg must match")
	}
	wrappingMode := strings.ToLower(strings.TrimSpace(crypto.KeyWrappingMode))
	if wrappingMode == "" {
		wrappingMode = KeyWrappingModeServerPublicKey
	}
	switch wrappingMode {
	case KeyWrappingModeServerKMS, KeyWrappingModeServerPublicKey, KeyWrappingModeCustomerKMS:
	default:
		return fmt.Errorf("unsupported key_wrapping_mode %q", wrappingMode)
	}
	if crypto.ServerKeyID == "" {
		return errors.New("server_key_id is required")
	}
	if crypto.EncryptedContentKey == "" && crypto.ContentKeyRef == "" && crypto.KMSKeyRef == "" {
		return errors.New("encrypted content key metadata is required")
	}
	if crypto.PayloadDigestAlg != "" && crypto.PayloadDigestAlg != "sha256" {
		return errors.New("payload_digest_alg must be sha256")
	}
	if err := validateEncryptedPayloadRef(e.PayloadRef); err != nil {
		return err
	}
	for _, attachment := range e.Attachments {
		if err := validateExchangeAttachment(attachment); err != nil {
			return err
		}
	}
	if !e.Policy.StructureSummaryOnly {
		return errors.New("exchange policy must require structure_summary_only")
	}
	if !e.Policy.DeletePayloadAfterRun {
		return errors.New("exchange policy must delete payload after run")
	}
	return nil
}

func validateEncryptedPayloadRef(ref EncryptedPayloadRef) error {
	switch ref.Kind {
	case PayloadRefKindInline:
		if ref.InlineCiphertext == "" {
			return errors.New("inline payload_ref requires inline_ciphertext")
		}
	case PayloadRefKindArtifact:
		if ref.ArtifactID == "" || ref.URI == "" {
			return errors.New("artifact payload_ref requires artifact_id and uri")
		}
	default:
		return errors.New("payload_ref kind must be inline or artifact")
	}
	if ref.SHA256 == "" {
		return errors.New("payload_ref sha256 is required")
	}
	if ref.SizeBytes <= 0 {
		return errors.New("payload_ref size_bytes must be positive")
	}
	if ref.Encrypted == false {
		return errors.New("payload_ref must be encrypted")
	}
	return nil
}

func validateExchangeAttachment(attachment ExchangeAttachment) error {
	if attachment.ID == "" || attachment.Role == "" || attachment.Kind == "" || attachment.URI == "" || attachment.SHA256 == "" {
		return errors.New("exchange attachment missing required metadata")
	}
	if !attachment.Encrypted {
		return errors.New("exchange attachment must be encrypted")
	}
	if isSensitiveArtifactRole(attachment.Role, attachment.Kind) && !attachment.Sensitive {
		return errors.New("sensitive exchange attachment must be marked sensitive")
	}
	return nil
}

func (r *RecordingResultPackage) ValidateDeliverySecurity() error {
	if r == nil {
		return errors.New("recording result package is nil")
	}
	if r.Delivery.RecipientKind == "" {
		return errors.New("result delivery recipient_kind is required")
	}
	if r.Delivery.RecipientKind != ResultRecipientAppInstallation && r.Delivery.RecipientKind != ResultRecipientOrganization {
		return errors.New("unsupported result delivery recipient_kind")
	}
	if r.Delivery.RecipientKeyID == "" {
		return errors.New("result delivery recipient_key_id is required")
	}
	if r.Delivery.EncryptionAlg == "" {
		return errors.New("result delivery encryption_alg is required")
	}
	if err := validatePackageArtifactDescriptor(r.Delivery.ResultPackageRef); err != nil {
		return fmt.Errorf("result package ref: %w", err)
	}
	for _, asset := range r.Delivery.AssetRefs {
		if err := validatePackageArtifactDescriptor(asset); err != nil {
			return err
		}
	}
	return nil
}

func validatePackageArtifactDescriptor(artifact PackageArtifactDescriptor) error {
	if artifact.ID == "" || artifact.Kind == "" || artifact.URI == "" || artifact.SHA256 == "" {
		return errors.New("artifact descriptor missing required metadata")
	}
	if !artifact.Encrypted {
		return errors.New("artifact descriptor must be encrypted")
	}
	if isSensitiveArtifactRole(artifact.Role, artifact.Kind) && !artifact.Sensitive {
		return errors.New("sensitive artifact descriptor must be marked sensitive")
	}
	return nil
}

func isSensitiveArtifactRole(role string, kind string) bool {
	value := strings.ToLower(role + " " + kind)
	for _, token := range []string{"failure", "trace", "dom", "accessibility", "recording", "result", "video", "screenshot", "secret"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
