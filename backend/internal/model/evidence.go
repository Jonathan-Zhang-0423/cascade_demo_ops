package model

import "time"

type EvidenceKind string

const (
	EvidenceKindUserInput      EvidenceKind = "user_input"
	EvidenceKindSourceCode     EvidenceKind = "source_code"
	EvidenceKindCodeSnapshot   EvidenceKind = "code_snapshot"
	EvidenceKindRequirementDoc EvidenceKind = "requirement_doc"
	EvidenceKindWebScreenshot  EvidenceKind = "webpage_screenshot"
	EvidenceKindScreenshotOCR  EvidenceKind = "screenshot_ocr"
	EvidenceKindVisionFinding  EvidenceKind = "vision_finding"
	EvidenceKindRepoSnapshot   EvidenceKind = "repo_snapshot"
	EvidenceKindBrowserScan    EvidenceKind = "browser_scan"
	EvidenceKindBrowserTrace   EvidenceKind = "browser_trace"
	EvidenceKindServerSnapshot EvidenceKind = "server_snapshot"
	EvidenceKindDocs           EvidenceKind = "docs"
	EvidenceKindReleaseNote    EvidenceKind = "release_note"
	EvidenceKindBrandKit       EvidenceKind = "brand_kit"
	EvidenceKindExecutionRun   EvidenceKind = "execution_run"
	EvidenceKindAssetReview    EvidenceKind = "asset_review"
)

type EvidenceRecord struct {
	ID           string         `json:"id"`
	ProjectID    string         `json:"project_id"`
	Kind         EvidenceKind   `json:"kind"`
	Source       EvidenceSource `json:"source"`
	CapturedAt   time.Time      `json:"captured_at,omitempty"`
	Summary      string         `json:"summary,omitempty"`
	Confidence   float64        `json:"confidence,omitempty"`
	ArtifactRefs []ArtifactRef  `json:"artifact_refs,omitempty"`
	Data         map[string]any `json:"data,omitempty"`
	Sensitive    bool           `json:"sensitive,omitempty"`
	Retention    *RetentionSpec `json:"retention,omitempty"`
}

type EvidenceSource struct {
	Type       string          `json:"type"`
	Name       string          `json:"name,omitempty"`
	URI        string          `json:"uri,omitempty"`
	CommitSHA  string          `json:"commit_sha,omitempty"`
	Branch     string          `json:"branch,omitempty"`
	Location   *SourceLocation `json:"location,omitempty"`
	SecretRef  string          `json:"secret_ref,omitempty"`
	CapturedBy string          `json:"captured_by,omitempty"`
}

type SourceLocation struct {
	Path      string `json:"path,omitempty"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
	Selector  string `json:"selector,omitempty"`
	Route     string `json:"route,omitempty"`
}

type EvidenceRef struct {
	ID         string       `json:"id"`
	Kind       EvidenceKind `json:"kind,omitempty"`
	Summary    string       `json:"summary,omitempty"`
	FieldPath  string       `json:"field_path,omitempty"`
	ArtifactID string       `json:"artifact_id,omitempty"`
	Confidence float64      `json:"confidence,omitempty"`
}

type ArtifactRef struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind,omitempty"`
	URI          string    `json:"uri"`
	MimeType     string    `json:"mime_type,omitempty"`
	Label        string    `json:"label,omitempty"`
	SHA256       string    `json:"sha256,omitempty"`
	SizeBytes    int64     `json:"size_bytes,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	Sensitive    bool      `json:"sensitive,omitempty"`
	SourceNodeID string    `json:"source_node_id,omitempty"`
}

type RetentionSpec struct {
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	DeleteAfterRun bool      `json:"delete_after_run,omitempty"`
	Reason         string    `json:"reason,omitempty"`
}
