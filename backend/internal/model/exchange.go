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
const ArtifactRoleFinalDemoVideo = "final_demo_video"
const ArtifactRoleRecordingOutput = "recording_output"
const ArtifactKindVideo = "video"
const ArtifactKindRecordingResultPackage = "recording_result_package"
const SandboxProfileDev = "dev"
const SandboxProfileMVPCloud = "mvp_cloud"
const SandboxProfileEnterprise = "enterprise"
const SandboxIsolationLocalSidecar = "local_sidecar"
const SandboxIsolationContainer = "per_job_container"
const SandboxIsolationMicroVM = "per_job_microvm"
const SandboxNetworkDenyAll = "deny_all"
const SandboxNetworkAllowedDomainsOnly = "allowed_domains_only"
const SandboxNetworkNoCustomerNetwork = "no_customer_network"
const SandboxFilesystemTmpfsWorkspace = "tmpfs_workspace"
const SandboxFilesystemReadOnlyRoot = "read_only_root"

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

type ResultDeliveryStatus string

const (
	ResultDeliveryStatusReady     ResultDeliveryStatus = "ready"
	ResultDeliveryStatusDelivered ResultDeliveryStatus = "delivered"
	ResultDeliveryStatusAcked     ResultDeliveryStatus = "acked"
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
	DevPlaintext     bool   `json:"dev_plaintext,omitempty"`
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
	SourceBindingSummary   *SourceBindingSummary            `json:"source_binding_summary,omitempty"`
	ConfidenceSummary      *PackageConfidenceSummary        `json:"confidence_summary,omitempty"`
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
	SandboxPolicy       *SandboxPolicy         `json:"sandbox_policy,omitempty"`
	Environment         map[string]string      `json:"environment,omitempty"`
}

type SandboxPolicy struct {
	Profile          string                  `json:"profile,omitempty"`
	IsolationMode    string                  `json:"isolation_mode,omitempty"`
	NetworkPolicy    SandboxNetworkPolicy    `json:"network_policy"`
	FilesystemPolicy SandboxFilesystemPolicy `json:"filesystem_policy"`
	ResourceLimits   SandboxResourceLimits   `json:"resource_limits"`
	BrowserPolicy    SandboxBrowserPolicy    `json:"browser_policy"`
	SecretPolicy     SandboxSecretPolicy     `json:"secret_policy"`
	ArtifactPolicy   SandboxArtifactPolicy   `json:"artifact_policy"`
	DiagnosticPolicy SandboxDiagnosticPolicy `json:"diagnostic_policy"`
	PolicyHashSHA256 string                  `json:"policy_hash_sha256,omitempty"`
}

type SandboxNetworkPolicy struct {
	Mode                    string   `json:"mode,omitempty"`
	AllowedDomains          []string `json:"allowed_domains,omitempty"`
	AllowedCascadeEndpoints []string `json:"allowed_cascade_endpoints,omitempty"`
	DeniedCIDRs             []string `json:"denied_cidrs,omitempty"`
	ProxyRequired           bool     `json:"proxy_required"`
	DNSPolicy               string   `json:"dns_policy,omitempty"`
}

type SandboxFilesystemPolicy struct {
	Mode               string   `json:"mode,omitempty"`
	WritablePaths      []string `json:"writable_paths,omitempty"`
	NoHostMount        bool     `json:"no_host_mount"`
	NoDockerSocket     bool     `json:"no_docker_socket"`
	DeleteTempAfterRun bool     `json:"delete_temp_after_run"`
}

type SandboxResourceLimits struct {
	MaxRuntimeSec int   `json:"max_runtime_sec,omitempty"`
	MaxMemoryMB   int   `json:"max_memory_mb,omitempty"`
	MaxCPUCount   int   `json:"max_cpu_count,omitempty"`
	MaxDiskMB     int64 `json:"max_disk_mb,omitempty"`
}

type SandboxBrowserPolicy struct {
	FreshContextPerRun bool     `json:"fresh_context_per_run"`
	DisableExtensions  bool     `json:"disable_extensions"`
	DisableDownloads   bool     `json:"disable_downloads"`
	TraceSources       bool     `json:"trace_sources"`
	AllowedPageMethods []string `json:"allowed_page_methods,omitempty"`
	AllowedContextAPIs []string `json:"allowed_context_apis,omitempty"`
}

type SandboxSecretPolicy struct {
	VaultOnly                bool     `json:"vault_only"`
	AllowedSecretRefs        []string `json:"allowed_secret_refs,omitempty"`
	InjectViaContextOnly     bool     `json:"inject_via_context_only"`
	ForbidEnvInjection       bool     `json:"forbid_env_injection"`
	RevokeAfterRun           bool     `json:"revoke_after_run"`
	RotationRequiredAfterRun bool     `json:"rotation_required_after_run"`
}

type SandboxArtifactPolicy struct {
	EncryptSensitiveArtifacts bool          `json:"encrypt_sensitive_artifacts"`
	SensitiveByDefault        bool          `json:"sensitive_by_default"`
	RecipientKind             string        `json:"recipient_kind,omitempty"`
	RecipientKeyID            string        `json:"recipient_key_id,omitempty"`
	Retention                 RetentionSpec `json:"retention,omitempty"`
	RequireChecksum           bool          `json:"require_checksum"`
}

type SandboxDiagnosticPolicy struct {
	RedactionRequired  bool     `json:"redaction_required"`
	ForbidFullHTML     bool     `json:"forbid_full_html"`
	StripHeaders       []string `json:"strip_headers,omitempty"`
	StripStorageKeys   []string `json:"strip_storage_keys,omitempty"`
	EncryptDiagnostics bool     `json:"encrypt_diagnostics"`
	ReturnRepairHints  bool     `json:"return_repair_hints"`
}

type SandboxExecutionMetadata struct {
	PolicyHashSHA256 string            `json:"policy_hash_sha256,omitempty"`
	Profile          string            `json:"profile,omitempty"`
	IsolationMode    string            `json:"isolation_mode,omitempty"`
	NetworkMode      string            `json:"network_mode,omitempty"`
	WorkerID         string            `json:"worker_id,omitempty"`
	ContainerID      string            `json:"container_id,omitempty"`
	MicroVMID        string            `json:"micro_vm_id,omitempty"`
	RuntimeVersions  map[string]string `json:"runtime_versions,omitempty"`
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
	ApprovalID                  string    `json:"approval_id"`
	ApprovedByUserID            string    `json:"approved_by_user_id,omitempty"`
	ApprovedAt                  time.Time `json:"approved_at"`
	PlanDigestSHA256            string    `json:"plan_digest_sha256"`
	ApprovalSubjectDigestSHA256 string    `json:"approval_subject_digest_sha256,omitempty"`
	ReviewedNodeIDs             []string  `json:"reviewed_node_ids,omitempty"`
	Notes                       []string  `json:"notes,omitempty"`
}

type RecordingResultPackage struct {
	ResultID              string                    `json:"result_id"`
	SourcePackageID       string                    `json:"source_package_id"`
	CloudJobID            string                    `json:"cloud_job_id"`
	SchemaVersion         string                    `json:"schema_version"`
	Status                RecordingResultStatus     `json:"status"`
	ExecutionTrace        *ExecutionTrace           `json:"execution_trace,omitempty"`
	StepResults           []StepResult              `json:"step_results,omitempty"`
	GeneratedAssets       []ArtifactRef             `json:"generated_assets,omitempty"`
	VerificationReport    VerificationReport        `json:"verification_report"`
	ExecutionRuntime      string                    `json:"execution_runtime,omitempty"`
	ValidationReports     []ValidationReport        `json:"validation_reports,omitempty"`
	PatchLedger           []RuntimePatchLedgerEntry `json:"patch_ledger,omitempty"`
	StageEventLogRef      *ArtifactRef              `json:"stage_event_log_ref,omitempty"`
	FailureDiagnostic     *ScriptFailureDiagnostic  `json:"failure_diagnostic,omitempty"`
	RepairRequest         *ScriptRepairRequest      `json:"repair_request,omitempty"`
	GraphPatchSuggestions []GraphPatch              `json:"graph_patch_suggestions,omitempty"`
	AuditTrail            CloudExecutionAuditTrail  `json:"audit_trail"`
	Delivery              ResultDelivery            `json:"delivery"`
	CreatedAt             time.Time                 `json:"created_at"`
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
	CloudWorkerID       string                    `json:"cloud_worker_id,omitempty"`
	StartedAt           time.Time                 `json:"started_at,omitempty"`
	CompletedAt         time.Time                 `json:"completed_at,omitempty"`
	RuntimeVersions     map[string]string         `json:"runtime_versions,omitempty"`
	Sandbox             *SandboxExecutionMetadata `json:"sandbox,omitempty"`
	SourcePackageDigest string                    `json:"source_package_digest,omitempty"`
	GraphDigest         string                    `json:"graph_digest,omitempty"`
	ExecutionIP         string                    `json:"execution_ip,omitempty"`
}

type ResultDelivery struct {
	ResultPackageRef   PackageArtifactDescriptor   `json:"result_package_ref"`
	AssetRefs          []PackageArtifactDescriptor `json:"asset_refs,omitempty"`
	RecipientKind      string                      `json:"recipient_kind,omitempty"`
	RecipientKeyID     string                      `json:"recipient_key_id,omitempty"`
	EncryptionAlg      string                      `json:"encryption_alg,omitempty"`
	ExpiresAt          time.Time                   `json:"expires_at,omitempty"`
	AckRequired        bool                        `json:"ack_required"`
	DeliveredAt        time.Time                   `json:"delivered_at,omitempty"`
	DownloadedAssetIDs []string                    `json:"downloaded_asset_ids,omitempty"`
	AckedAt            time.Time                   `json:"acked_at,omitempty"`
	AckedByInstallID   string                      `json:"acked_by_install_id,omitempty"`
	ReceivedAssetIDs   []string                    `json:"received_asset_ids,omitempty"`
	VerifiedChecksums  bool                        `json:"verified_checksums,omitempty"`
}

func (r *RecordingResultPackage) ValidateStatusContract() error {
	if r == nil {
		return errors.New("recording result package is nil")
	}
	if r.SchemaVersion != RecordingResultPackageSchemaVersion {
		return errors.New("unsupported recording result package schema version")
	}
	for _, report := range r.ValidationReports {
		if err := report.Validate(); err != nil {
			return fmt.Errorf("invalid validation report: %w", err)
		}
		if report.SourcePackageID != r.SourcePackageID {
			return errors.New("validation report source_package_id does not match recording result")
		}
	}
	for _, entry := range r.PatchLedger {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("invalid patch ledger entry: %w", err)
		}
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
		if artifact.Metadata != nil && artifact.Metadata["dev_local_artifact"] == true {
			continue
		}
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
	UploadID              string                    `json:"upload_id"`
	ServerPublicKeyID     string                    `json:"server_public_key_id"`
	ServerPublicKeyAlg    string                    `json:"server_public_key_alg,omitempty"`
	ServerPublicKeys      []ExchangeServerPublicKey `json:"server_public_keys,omitempty"`
	KeyWrappingModes      []string                  `json:"key_wrapping_modes,omitempty"`
	SupportedCryptoSuites []string                  `json:"supported_crypto_suites,omitempty"`
	SupportedCompression  []string                  `json:"supported_compression,omitempty"`
	RequiredSignatureAlg  string                    `json:"required_signature_alg,omitempty"`
	InstallationRequired  bool                      `json:"installation_required,omitempty"`
	SessionExpiresAt      time.Time                 `json:"session_expires_at,omitempty"`
	ResultRecipientKeyID  string                    `json:"result_recipient_key_id,omitempty"`
	CascadeExecutionIPs   []string                  `json:"cascade_execution_ips"`
	MaxEnvelopeBytes      int64                     `json:"max_envelope_bytes"`
	MaxAttachmentBytes    int64                     `json:"max_attachment_bytes"`
	ExpiresAt             time.Time                 `json:"expires_at"`
}

type ExchangeBootstrapDiscoveryResponse struct {
	SchemaVersion         string                    `json:"schema_version"`
	ExchangeBaseURL       string                    `json:"exchange_base_url"`
	ServerKeyset          []ExchangeServerPublicKey `json:"server_keyset"`
	SupportedCryptoSuites []string                  `json:"supported_crypto_suites"`
	SupportedCompression  []string                  `json:"supported_compression,omitempty"`
	PairingMethods        []ExchangePairingMethod   `json:"pairing_methods"`
	Challenge             ExchangePairingChallenge  `json:"challenge"`
	ExpiresAt             time.Time                 `json:"expires_at"`
	Environment           string                    `json:"environment"`
	Terms                 ExchangeBootstrapTerms    `json:"terms"`
}

type ExchangeServerPublicKey struct {
	KeyID     string    `json:"key_id"`
	Alg       string    `json:"alg"`
	PublicKey string    `json:"public_key"`
	NotBefore time.Time `json:"not_before,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type ExchangePairingMethod struct {
	Kind        string `json:"kind"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

type ExchangePairingChallenge struct {
	ChallengeID string    `json:"challenge_id"`
	Nonce       string    `json:"nonce"`
	Alg         string    `json:"alg"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type ExchangeBootstrapTerms struct {
	RequireHTTPS bool `json:"require_https"`
	DevPlaintext bool `json:"dev_plaintext,omitempty"`
}

type AppInstallationPublicKey struct {
	KeyID     string `json:"key_id"`
	Alg       string `json:"alg"`
	PublicKey string `json:"public_key"`
	Purpose   string `json:"purpose,omitempty"`
}

type AppInstallationRegisterRequest struct {
	InstallID          string                   `json:"install_id"`
	DeviceID           string                   `json:"device_id,omitempty"`
	OrgID              string                   `json:"org_id,omitempty"`
	ProjectID          string                   `json:"project_id,omitempty"`
	AppVersion         string                   `json:"app_version,omitempty"`
	RuntimeProfile     string                   `json:"runtime_profile,omitempty"`
	SigningPublicKey   AppInstallationPublicKey `json:"signing_public_key"`
	ResultPublicKey    AppInstallationPublicKey `json:"result_public_key"`
	ChallengeID        string                   `json:"challenge_id"`
	ChallengeSignature string                   `json:"challenge_signature"`
}

type AppInstallationSessionRequest struct {
	InstallID          string `json:"install_id"`
	DeviceID           string `json:"device_id,omitempty"`
	ChallengeID        string `json:"challenge_id,omitempty"`
	ChallengeSignature string `json:"challenge_signature,omitempty"`
}

type AppInstallationSessionResponse struct {
	InstallID            string    `json:"install_id"`
	SessionID            string    `json:"session_id"`
	SessionToken         string    `json:"session_token"`
	ExpiresAt            time.Time `json:"expires_at"`
	OrgID                string    `json:"org_id,omitempty"`
	ProjectID            string    `json:"project_id,omitempty"`
	ServerKeyID          string    `json:"server_key_id"`
	ResultRecipientKeyID string    `json:"result_recipient_key_id"`
	AuthMode             string    `json:"auth_mode"`
}

type AppInstallationRevokeRequest struct {
	InstallID string `json:"install_id"`
	Reason    string `json:"reason,omitempty"`
}

type ExecutionPackageUploadRequest struct {
	UploadID   string              `json:"upload_id"`
	Envelope   ExchangeEnvelope    `json:"envelope"`
	PayloadRef EncryptedPayloadRef `json:"payload_ref,omitempty"`
}

type ExecutionPackageUploadResponse struct {
	ExchangePackageID string                `json:"exchange_package_id"`
	CloudJobID        string                `json:"cloud_job_id"`
	Status            ExchangePackageStatus `json:"status"`
}

type ExecutionPackageStatusResponse struct {
	ExchangePackageID string                   `json:"exchange_package_id"`
	CloudJobID        string                   `json:"cloud_job_id,omitempty"`
	Status            ExchangePackageStatus    `json:"status"`
	Stage             string                   `json:"stage,omitempty"`
	Message           string                   `json:"message,omitempty"`
	ProgressPercent   int                      `json:"progress_percent,omitempty"`
	StageHistory      []ExecutionStageEvent    `json:"stage_history,omitempty"`
	ResultPackageID   string                   `json:"result_package_id,omitempty"`
	ResultSummary     *ExecutionResultSummary  `json:"result_summary,omitempty"`
	FailureSummary    *ExecutionFailureSummary `json:"failure_summary,omitempty"`
	Error             *AgentError              `json:"error,omitempty"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

type ExecutionPackageListResponse struct {
	Items []ExecutionPackageListItem `json:"items"`
}

type ExecutionPackageListItem struct {
	ExchangePackageID string                   `json:"exchange_package_id"`
	CloudJobID        string                   `json:"cloud_job_id,omitempty"`
	OrgID             string                   `json:"org_id"`
	ProjectID         string                   `json:"project_id"`
	PackageID         string                   `json:"package_id,omitempty"`
	Status            ExchangePackageStatus    `json:"status"`
	Stage             string                   `json:"stage,omitempty"`
	Message           string                   `json:"message,omitempty"`
	ProgressPercent   int                      `json:"progress_percent,omitempty"`
	ResultPackageID   string                   `json:"result_package_id,omitempty"`
	ResultSummary     *ExecutionResultSummary  `json:"result_summary,omitempty"`
	FailureSummary    *ExecutionFailureSummary `json:"failure_summary,omitempty"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

type ExecutionStageEvent struct {
	EventID         string                `json:"event_id,omitempty"`
	Stage           string                `json:"stage"`
	Status          ExchangePackageStatus `json:"status,omitempty"`
	Message         string                `json:"message,omitempty"`
	ProgressPercent int                   `json:"progress_percent,omitempty"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

type ResultReviewDecision string

const (
	ResultReviewApproved          ResultReviewDecision = "approved"
	ResultReviewReeditRequested   ResultReviewDecision = "reedit_requested"
	ResultReviewRerecordRequested ResultReviewDecision = "rerecord_requested"
)

type ResultReviewAnnotation struct {
	TimeMS  int64  `json:"time_ms"`
	Comment string `json:"comment"`
}

type ResultReviewRequest struct {
	IdempotencyKey    string                   `json:"idempotency_key"`
	ReviewerInstallID string                   `json:"reviewer_install_id,omitempty"`
	Decision          ResultReviewDecision     `json:"decision"`
	Summary           string                   `json:"summary,omitempty"`
	Annotations       []ResultReviewAnnotation `json:"annotations,omitempty"`
	ReviewedAt        time.Time                `json:"reviewed_at,omitempty"`
}

type ResultReviewRecord struct {
	ReviewID          string                   `json:"review_id"`
	ResultPackageID   string                   `json:"result_package_id"`
	IdempotencyKey    string                   `json:"idempotency_key"`
	ReviewerInstallID string                   `json:"reviewer_install_id,omitempty"`
	Decision          ResultReviewDecision     `json:"decision"`
	Summary           string                   `json:"summary,omitempty"`
	Annotations       []ResultReviewAnnotation `json:"annotations,omitempty"`
	ReviewedAt        time.Time                `json:"reviewed_at"`
}

type ResultRevisionAction string

const (
	ResultRevisionAuto     ResultRevisionAction = "auto"
	ResultRevisionReedit   ResultRevisionAction = "reedit"
	ResultRevisionRerecord ResultRevisionAction = "rerecord"
)

type ResultRevisionIssue struct {
	Kind    string `json:"kind"`
	TimeMS  int64  `json:"time_ms,omitempty"`
	Comment string `json:"comment"`
}

type ResultRevisionRequest struct {
	IdempotencyKey       string                `json:"idempotency_key"`
	RequestedByInstallID string                `json:"requested_by_install_id,omitempty"`
	RequestedAction      ResultRevisionAction  `json:"requested_action,omitempty"`
	Summary              string                `json:"summary,omitempty"`
	Issues               []ResultRevisionIssue `json:"issues,omitempty"`
	RequestedAt          time.Time             `json:"requested_at,omitempty"`
}

type ResultRevisionRecord struct {
	RevisionID           string                `json:"revision_id"`
	ResultPackageID      string                `json:"result_package_id"`
	IdempotencyKey       string                `json:"idempotency_key"`
	RequestedByInstallID string                `json:"requested_by_install_id,omitempty"`
	RequestedAction      ResultRevisionAction  `json:"requested_action"`
	ResolvedAction       ResultRevisionAction  `json:"resolved_action"`
	Status               string                `json:"status"`
	Summary              string                `json:"summary,omitempty"`
	Issues               []ResultRevisionIssue `json:"issues,omitempty"`
	RequestedAt          time.Time             `json:"requested_at"`
}

type ExecutionFailureSummary struct {
	Code                 string `json:"code,omitempty"`
	Message              string `json:"message,omitempty"`
	FailedStage          string `json:"failed_stage,omitempty"`
	FailedNodeID         string `json:"failed_node_id,omitempty"`
	CurrentURL           string `json:"current_url,omitempty"`
	PageTitle            string `json:"page_title,omitempty"`
	FailureScreenshotURI string `json:"failure_screenshot_uri,omitempty"`
	FailureTraceURI      string `json:"failure_trace_uri,omitempty"`
	Retryable            bool   `json:"retryable,omitempty"`
}

type ExecutionResultSummary struct {
	ResultID            string                 `json:"result_id,omitempty"`
	ResultStatus        RecordingResultStatus  `json:"result_status,omitempty"`
	DeliveryStatus      ResultDeliveryStatus   `json:"delivery_status,omitempty"`
	PassRate            float64                `json:"pass_rate,omitempty"`
	StepCount           int                    `json:"step_count,omitempty"`
	PassedStepCount     int                    `json:"passed_step_count,omitempty"`
	FailedStepCount     int                    `json:"failed_step_count,omitempty"`
	GeneratedAssetCount int                    `json:"generated_asset_count,omitempty"`
	DemoVideoCount      int                    `json:"demo_video_count,omitempty"`
	ScreenshotCount     int                    `json:"screenshot_count,omitempty"`
	RawRecordingCount   int                    `json:"raw_recording_count,omitempty"`
	TraceCount          int                    `json:"trace_count,omitempty"`
	PrimaryDemoVideoURI string                 `json:"primary_demo_video_uri,omitempty"`
	RawRecordingURI     string                 `json:"raw_recording_uri,omitempty"`
	AckRequired         bool                   `json:"ack_required,omitempty"`
	DeliveredAt         time.Time              `json:"delivered_at,omitempty"`
	AckedAt             time.Time              `json:"acked_at,omitempty"`
	ExpiresAt           time.Time              `json:"expires_at,omitempty"`
	Deliverables        []ExecutionDeliverable `json:"deliverables,omitempty"`
	// Validation is a redacted, App-consumable summary of Server-side Browser
	// Agent verification. Full evidence remains in the result package.
	Validation *ExecutionValidationSummary `json:"validation,omitempty"`
}

// ExecutionValidationSummary lets the App distinguish a rendered file from a
// result that also has complete, runtime-derived Browser Agent verification.
// It deliberately contains counts and decisions only, never page data or
// credential-bearing diagnostic details.
type ExecutionValidationSummary struct {
	Runtime                  string             `json:"runtime,omitempty"`
	Status                   string             `json:"status"`
	ValidationReportCount    int                `json:"validation_report_count"`
	PreExecutionReportCount  int                `json:"pre_execution_report_count"`
	RuntimeStageReportCount  int                `json:"runtime_stage_report_count"`
	PostExecutionReportCount int                `json:"post_execution_report_count"`
	LatestDecision           ValidationDecision `json:"latest_decision,omitempty"`
	RealObservedStepCount    int                `json:"real_observed_step_count"`
	StageEventLogAvailable   bool               `json:"stage_event_log_available"`
}

type ExecutionDeliverable struct {
	ID            string `json:"id,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Role          string `json:"role,omitempty"`
	URI           string `json:"uri,omitempty"`
	DownloadURL   string `json:"download_url,omitempty"`
	MimeType      string `json:"mime_type,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	SourceNodeID  string `json:"source_node_id,omitempty"`
	IncludeInDemo bool   `json:"include_in_demo,omitempty"`
	Sensitive     bool   `json:"sensitive,omitempty"`
}

type ResultPackageAckRequest struct {
	ResultPackageID     string    `json:"result_package_id"`
	AckedByInstallID    string    `json:"acked_by_install_id,omitempty"`
	ReceivedAssetIDs    []string  `json:"received_asset_ids,omitempty"`
	VerifiedChecksums   bool      `json:"verified_checksums,omitempty"`
	ChecksumMismatchIDs []string  `json:"checksum_mismatch_ids,omitempty"`
	AckedAt             time.Time `json:"acked_at"`
}

type ResultPackageAckResponse struct {
	ResultPackageID   string                `json:"result_package_id"`
	Status            RecordingResultStatus `json:"status"`
	DeliveryStatus    ResultDeliveryStatus  `json:"delivery_status,omitempty"`
	Retention         RetentionSpec         `json:"retention"`
	AckedAt           time.Time             `json:"acked_at,omitempty"`
	AckedByInstallID  string                `json:"acked_by_install_id,omitempty"`
	ReceivedAssetIDs  []string              `json:"received_asset_ids,omitempty"`
	VerifiedChecksums bool                  `json:"verified_checksums,omitempty"`
}

type ResultPackageListResponse struct {
	Items []ResultPackageListItem `json:"items"`
}

type ResultPackageListItem struct {
	ResultPackageID   string                   `json:"result_package_id"`
	ResultID          string                   `json:"result_id,omitempty"`
	ExchangePackageID string                   `json:"exchange_package_id"`
	SourcePackageID   string                   `json:"source_package_id,omitempty"`
	CloudJobID        string                   `json:"cloud_job_id,omitempty"`
	OrgID             string                   `json:"org_id"`
	ProjectID         string                   `json:"project_id"`
	Status            RecordingResultStatus    `json:"status"`
	DeliveryStatus    ResultDeliveryStatus     `json:"delivery_status,omitempty"`
	ResultSummary     *ExecutionResultSummary  `json:"result_summary,omitempty"`
	FailureSummary    *ExecutionFailureSummary `json:"failure_summary,omitempty"`
	CreatedAt         time.Time                `json:"created_at"`
	ExpiresAt         time.Time                `json:"expires_at,omitempty"`
	AckRequired       bool                     `json:"ack_required,omitempty"`
	DeliveredAt       time.Time                `json:"delivered_at,omitempty"`
	AckedAt           time.Time                `json:"acked_at,omitempty"`
	AckedByInstallID  string                   `json:"acked_by_install_id,omitempty"`
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

func DefaultSandboxPolicyForRunSpec(spec RecordingRunSpec) SandboxPolicy {
	maxRuntime := spec.Timeline.TargetDurationSec + 120
	if maxRuntime < 300 {
		maxRuntime = 300
	}
	policy := SandboxPolicy{
		Profile:       SandboxProfileMVPCloud,
		IsolationMode: SandboxIsolationContainer,
		NetworkPolicy: SandboxNetworkPolicy{
			Mode:                    SandboxNetworkAllowedDomainsOnly,
			AllowedDomains:          append([]string{}, spec.AllowedDomains...),
			AllowedCascadeEndpoints: []string{"artifact", "kms", "vault"},
			DeniedCIDRs:             []string{"169.254.169.254/32", "127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
			ProxyRequired:           true,
			DNSPolicy:               "proxy_resolved",
		},
		FilesystemPolicy: SandboxFilesystemPolicy{
			Mode:               SandboxFilesystemReadOnlyRoot,
			WritablePaths:      []string{"/workspace", "/tmp"},
			NoHostMount:        true,
			NoDockerSocket:     true,
			DeleteTempAfterRun: true,
		},
		ResourceLimits: SandboxResourceLimits{
			MaxRuntimeSec: maxRuntime,
			MaxMemoryMB:   2048,
			MaxCPUCount:   2,
			MaxDiskMB:     4096,
		},
		BrowserPolicy: SandboxBrowserPolicy{
			FreshContextPerRun: true,
			DisableExtensions:  true,
			DisableDownloads:   true,
			TraceSources:       false,
			AllowedPageMethods: []string{
				"goto", "click", "fill", "selectOption", "setInputFiles", "waitForTimeout", "waitForLoadState", "locator",
			},
			AllowedContextAPIs: []string{"ctx.page", "ctx.secrets", "ctx.capture", "ctx.assert", "ctx.log"},
		},
		SecretPolicy: SandboxSecretPolicy{
			VaultOnly:                true,
			InjectViaContextOnly:     true,
			ForbidEnvInjection:       true,
			RevokeAfterRun:           true,
			RotationRequiredAfterRun: true,
		},
		ArtifactPolicy: SandboxArtifactPolicy{
			EncryptSensitiveArtifacts: true,
			SensitiveByDefault:        true,
			RecipientKind:             ResultRecipientAppInstallation,
			RequireChecksum:           true,
		},
		DiagnosticPolicy: SandboxDiagnosticPolicy{
			RedactionRequired:  true,
			ForbidFullHTML:     true,
			StripHeaders:       []string{"authorization", "cookie", "set-cookie"},
			StripStorageKeys:   []string{"localStorage", "sessionStorage"},
			EncryptDiagnostics: true,
			ReturnRepairHints:  true,
		},
	}
	policy.PolicyHashSHA256 = sandboxPolicyHash(policy)
	return policy
}

func ResolveSandboxPolicy(spec RecordingRunSpec, bundle *ExecutableRecordingScriptBundle) SandboxPolicy {
	if spec.SandboxPolicy != nil {
		policy := *spec.SandboxPolicy
		normalizeSandboxPolicy(&policy, spec)
		return policy
	}
	policy := DefaultSandboxPolicyForRunSpec(spec)
	if bundle != nil {
		if len(bundle.SecurityPolicy.AllowedDomains) > 0 {
			policy.NetworkPolicy.AllowedDomains = append([]string{}, bundle.SecurityPolicy.AllowedDomains...)
		}
		if len(bundle.SecurityPolicy.AllowedContextAPIs) > 0 {
			policy.BrowserPolicy.AllowedContextAPIs = append([]string{}, bundle.SecurityPolicy.AllowedContextAPIs...)
		}
		if len(bundle.SecurityPolicy.AllowedPageMethods) > 0 {
			policy.BrowserPolicy.AllowedPageMethods = append([]string{}, bundle.SecurityPolicy.AllowedPageMethods...)
		}
		policy.SecretPolicy.AllowedSecretRefs = append([]string{}, bundle.SecurityPolicy.SecretRefs...)
	}
	normalizeSandboxPolicy(&policy, spec)
	return policy
}

func sandboxExecutionMetadataFromPolicy(policy SandboxPolicy) SandboxExecutionMetadata {
	return SandboxExecutionMetadata{
		PolicyHashSHA256: policy.PolicyHashSHA256,
		Profile:          policy.Profile,
		IsolationMode:    policy.IsolationMode,
		NetworkMode:      policy.NetworkPolicy.Mode,
	}
}

func ValidateSandboxPolicyForPackage(pkg *ClientExecutionPackage) error {
	if pkg == nil {
		return errors.New("client execution package is nil")
	}
	if explicitPolicy := pkg.RecordingRunSpec.SandboxPolicy; explicitPolicy != nil {
		profile := explicitPolicy.Profile
		if profile == "" {
			profile = SandboxProfileMVPCloud
		}
		if profile != SandboxProfileDev && !explicitPolicy.NetworkPolicy.ProxyRequired {
			return errors.New("cloud sandbox network egress proxy is required")
		}
	}
	policy := ResolveSandboxPolicy(pkg.RecordingRunSpec, pkg.ExecutableScriptBundle)
	if policy.IsolationMode == "" {
		return errors.New("sandbox isolation_mode is required")
	}
	switch policy.IsolationMode {
	case SandboxIsolationLocalSidecar, SandboxIsolationContainer, SandboxIsolationMicroVM:
	default:
		return fmt.Errorf("unsupported sandbox isolation_mode %q", policy.IsolationMode)
	}
	if policy.NetworkPolicy.Mode == "" {
		return errors.New("sandbox network_policy.mode is required")
	}
	switch policy.NetworkPolicy.Mode {
	case SandboxNetworkDenyAll, SandboxNetworkAllowedDomainsOnly, SandboxNetworkNoCustomerNetwork:
	default:
		return fmt.Errorf("unsupported sandbox network_policy.mode %q", policy.NetworkPolicy.Mode)
	}
	if policy.NetworkPolicy.Mode == SandboxNetworkAllowedDomainsOnly && len(policy.NetworkPolicy.AllowedDomains) == 0 {
		return errors.New("sandbox allowed_domains is required for allowed_domains_only network policy")
	}
	if !allStringsAllowed(policy.NetworkPolicy.AllowedDomains, pkg.RecordingRunSpec.AllowedDomains) {
		return errors.New("sandbox allowed_domains exceed recording_run_spec.allowed_domains")
	}
	if !policy.NetworkPolicy.ProxyRequired && policy.Profile != SandboxProfileDev {
		return errors.New("cloud sandbox network egress proxy is required")
	}
	if !policy.FilesystemPolicy.NoHostMount || !policy.FilesystemPolicy.NoDockerSocket {
		return errors.New("sandbox filesystem policy must disable host mounts and docker socket")
	}
	if policy.Profile != SandboxProfileDev && policy.IsolationMode == SandboxIsolationLocalSidecar {
		return errors.New("local_sidecar isolation is only allowed for dev profile")
	}
	if policy.ResourceLimits.MaxRuntimeSec <= 0 || policy.ResourceLimits.MaxMemoryMB <= 0 || policy.ResourceLimits.MaxDiskMB <= 0 {
		return errors.New("sandbox resource limits must include runtime, memory, and disk")
	}
	if !policy.BrowserPolicy.FreshContextPerRun || !policy.BrowserPolicy.DisableExtensions || !policy.BrowserPolicy.DisableDownloads || policy.BrowserPolicy.TraceSources {
		return errors.New("sandbox browser policy must use fresh context, disable extensions/downloads, and disable trace sources")
	}
	if !policy.SecretPolicy.VaultOnly || !policy.SecretPolicy.InjectViaContextOnly || !policy.SecretPolicy.ForbidEnvInjection {
		return errors.New("sandbox secret policy must use vault-only context injection")
	}
	if !policy.ArtifactPolicy.EncryptSensitiveArtifacts || !policy.ArtifactPolicy.RequireChecksum {
		return errors.New("sandbox artifact policy must encrypt sensitive artifacts and require checksums")
	}
	if !policy.DiagnosticPolicy.RedactionRequired || !policy.DiagnosticPolicy.ForbidFullHTML || !policy.DiagnosticPolicy.EncryptDiagnostics {
		return errors.New("sandbox diagnostic policy must require redaction, forbid full HTML, and encrypt diagnostics")
	}
	return nil
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

func (e *ExchangeEnvelope) ValidateMetadataForEncryptedUpload(now time.Time, seenNonces map[string]bool) error {
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
	if e.Crypto.PayloadDigestSHA256 == "" {
		return errors.New("exchange envelope payload digest is required")
	}
	if e.Crypto.CiphertextDigestSHA256 == "" {
		return errors.New("exchange envelope ciphertext digest is required")
	}
	if e.PayloadRef.SHA256 != "" && e.Crypto.CiphertextDigestSHA256 != e.PayloadRef.SHA256 {
		return errors.New("exchange envelope ciphertext digest does not match payload_ref sha256")
	}
	if e.Crypto.Signature == "" || e.Crypto.SignatureKeyID == "" || e.Crypto.SignatureAlg == "" {
		return errors.New("exchange envelope signature metadata is required")
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

func normalizeSandboxPolicy(policy *SandboxPolicy, spec RecordingRunSpec) {
	if policy == nil {
		return
	}
	if policy.Profile == "" {
		policy.Profile = SandboxProfileMVPCloud
	}
	if policy.IsolationMode == "" {
		if policy.Profile == SandboxProfileDev {
			policy.IsolationMode = SandboxIsolationLocalSidecar
		} else {
			policy.IsolationMode = SandboxIsolationContainer
		}
	}
	if policy.NetworkPolicy.Mode == "" {
		policy.NetworkPolicy.Mode = SandboxNetworkAllowedDomainsOnly
	}
	if len(policy.NetworkPolicy.AllowedDomains) == 0 {
		policy.NetworkPolicy.AllowedDomains = append([]string{}, spec.AllowedDomains...)
	}
	if len(policy.NetworkPolicy.DeniedCIDRs) == 0 {
		policy.NetworkPolicy.DeniedCIDRs = []string{"169.254.169.254/32", "127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	}
	if policy.Profile != SandboxProfileDev {
		policy.NetworkPolicy.ProxyRequired = true
	}
	if policy.FilesystemPolicy.Mode == "" {
		policy.FilesystemPolicy.Mode = SandboxFilesystemReadOnlyRoot
	}
	if len(policy.FilesystemPolicy.WritablePaths) == 0 {
		policy.FilesystemPolicy.WritablePaths = []string{"/workspace", "/tmp"}
	}
	if policy.ResourceLimits.MaxRuntimeSec <= 0 {
		policy.ResourceLimits.MaxRuntimeSec = 300
	}
	if policy.ResourceLimits.MaxMemoryMB <= 0 {
		policy.ResourceLimits.MaxMemoryMB = 2048
	}
	if policy.ResourceLimits.MaxCPUCount <= 0 {
		policy.ResourceLimits.MaxCPUCount = 2
	}
	if policy.ResourceLimits.MaxDiskMB <= 0 {
		policy.ResourceLimits.MaxDiskMB = 4096
	}
	if len(policy.BrowserPolicy.AllowedPageMethods) == 0 {
		policy.BrowserPolicy.AllowedPageMethods = []string{"goto", "click", "fill", "selectOption", "setInputFiles", "waitForTimeout", "waitForLoadState", "locator"}
	}
	if len(policy.BrowserPolicy.AllowedContextAPIs) == 0 {
		policy.BrowserPolicy.AllowedContextAPIs = []string{"ctx.page", "ctx.secrets", "ctx.capture", "ctx.assert", "ctx.log"}
	}
	if policy.ArtifactPolicy.RecipientKind == "" {
		policy.ArtifactPolicy.RecipientKind = ResultRecipientAppInstallation
	}
	if len(policy.DiagnosticPolicy.StripHeaders) == 0 {
		policy.DiagnosticPolicy.StripHeaders = []string{"authorization", "cookie", "set-cookie"}
	}
	if len(policy.DiagnosticPolicy.StripStorageKeys) == 0 {
		policy.DiagnosticPolicy.StripStorageKeys = []string{"localStorage", "sessionStorage"}
	}
	policy.PolicyHashSHA256 = sandboxPolicyHash(*policy)
}

func sandboxPolicyHash(policy SandboxPolicy) string {
	copy := policy
	copy.PolicyHashSHA256 = ""
	digest, err := DigestCanonicalJSON(copy)
	if err != nil {
		return ""
	}
	return digest
}

func allStringsAllowed(values []string, allowed []string) bool {
	allowedSet := map[string]bool{}
	for _, value := range allowed {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized != "" {
			allowedSet[normalized] = true
		}
	}
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" || !allowedSet[normalized] {
			return false
		}
	}
	return true
}
