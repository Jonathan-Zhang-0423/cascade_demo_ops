package model

import "time"

const MultimodalUnderstandingReportSchemaVersion = "demoops.multimodal_understanding_report.v1"

type RequirementBrief struct {
	ID             string        `json:"id"`
	ProjectID      string        `json:"project_id,omitempty"`
	SchemaVersion  string        `json:"schema_version,omitempty"`
	Scenario       string        `json:"scenario,omitempty"`
	TargetAudience string        `json:"target_audience,omitempty"`
	Objective      string        `json:"objective,omitempty"`
	PrimaryOutcome string        `json:"primary_outcome,omitempty"`
	MustShow       []string      `json:"must_show,omitempty"`
	MustNotShow    []string      `json:"must_not_show,omitempty"`
	ForbiddenPages []string      `json:"forbidden_pages,omitempty"`
	ForbiddenData  []string      `json:"forbidden_data,omitempty"`
	UseCases       []DemoUseCase `json:"use_cases,omitempty"`
	RequiredAssets []AssetKind   `json:"required_assets,omitempty"`
	BrandTone      string        `json:"brand_tone,omitempty"`
	EvidenceRefs   []EvidenceRef `json:"evidence_refs,omitempty"`
	Confidence     float64       `json:"confidence,omitempty"`
	CreatedAt      time.Time     `json:"created_at,omitempty"`
}

type CodeUnderstandingSnapshot struct {
	ID                 string                  `json:"id"`
	ProjectID          string                  `json:"project_id,omitempty"`
	SchemaVersion      string                  `json:"schema_version,omitempty"`
	RepositoryID       string                  `json:"repository_id,omitempty"`
	URI                string                  `json:"uri,omitempty"`
	Branch             string                  `json:"branch,omitempty"`
	CommitSHA          string                  `json:"commit_sha,omitempty"`
	Languages          []string                `json:"languages,omitempty"`
	Frameworks         []string                `json:"frameworks,omitempty"`
	EntrypointHashes   []string                `json:"entrypoint_hashes,omitempty"`
	Routes             []RouteInsight          `json:"routes,omitempty"`
	Components         []ComponentInsight      `json:"components,omitempty"`
	Selectors          []SelectorInsight       `json:"selectors,omitempty"`
	APIEndpoints       []APIEndpointInsight    `json:"api_endpoints,omitempty"`
	DataModels         []DataModelInsight      `json:"data_models,omitempty"`
	SensitiveFields    []SensitiveFieldFinding `json:"sensitive_fields,omitempty"`
	SourceDigestSHA256 string                  `json:"source_digest_sha256,omitempty"`
	FileCount          int                     `json:"file_count,omitempty"`
	PathDigests        []PathDigest            `json:"path_digests,omitempty"`
	EvidenceRefs       []EvidenceRef           `json:"evidence_refs,omitempty"`
	Summary            string                  `json:"summary,omitempty"`
	CreatedAt          time.Time               `json:"created_at,omitempty"`
}

type RouteInsight struct {
	ID             string        `json:"id"`
	Path           string        `json:"path"`
	Name           string        `json:"name,omitempty"`
	SourcePathHash string        `json:"source_path_hash_sha256,omitempty"`
	ComponentRefs  []string      `json:"component_refs,omitempty"`
	AuthRequired   bool          `json:"auth_required,omitempty"`
	EvidenceRefs   []EvidenceRef `json:"evidence_refs,omitempty"`
	Confidence     float64       `json:"confidence,omitempty"`
}

type ComponentInsight struct {
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	Kind               string        `json:"kind,omitempty"`
	FilePathHashSHA256 string        `json:"file_path_hash_sha256,omitempty"`
	SelectorHints      []string      `json:"selector_hints,omitempty"`
	ActionLabels       []string      `json:"action_labels,omitempty"`
	EvidenceRefs       []EvidenceRef `json:"evidence_refs,omitempty"`
	Confidence         float64       `json:"confidence,omitempty"`
}

type SelectorInsight struct {
	Kind               string        `json:"kind"`
	Value              string        `json:"value"`
	FilePathHashSHA256 string        `json:"file_path_hash_sha256,omitempty"`
	StabilityScore     float64       `json:"stability_score,omitempty"`
	Confidence         float64       `json:"confidence,omitempty"`
	EvidenceRefs       []EvidenceRef `json:"evidence_refs,omitempty"`
}

type APIEndpointInsight struct {
	ID                 string        `json:"id"`
	Method             string        `json:"method,omitempty"`
	Path               string        `json:"path"`
	FilePathHashSHA256 string        `json:"file_path_hash_sha256,omitempty"`
	EvidenceRefs       []EvidenceRef `json:"evidence_refs,omitempty"`
	Confidence         float64       `json:"confidence,omitempty"`
}

type DataModelInsight struct {
	ID                   string        `json:"id"`
	Name                 string        `json:"name"`
	Kind                 string        `json:"kind,omitempty"`
	Fields               []DataField   `json:"fields,omitempty"`
	SourcePathHashSHA256 string        `json:"source_path_hash_sha256,omitempty"`
	EvidenceRefs         []EvidenceRef `json:"evidence_refs,omitempty"`
	Confidence           float64       `json:"confidence,omitempty"`
}

type SensitiveFieldFinding struct {
	Name         string        `json:"name"`
	Kind         string        `json:"kind,omitempty"`
	Reason       string        `json:"reason,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type PageUnderstandingSnapshot struct {
	ID              string              `json:"id"`
	ProjectID       string              `json:"project_id,omitempty"`
	SchemaVersion   string              `json:"schema_version,omitempty"`
	URL             string              `json:"url,omitempty"`
	Title           string              `json:"title,omitempty"`
	PageRole        string              `json:"page_role,omitempty"`
	ScreenshotRef   *ArtifactRef        `json:"screenshot_ref,omitempty"`
	OCRText         string              `json:"ocr_text,omitempty"`
	VisionSummary   string              `json:"vision_summary,omitempty"`
	Actions         []PageActionInsight `json:"actions,omitempty"`
	StableSelectors []SelectorCandidate `json:"stable_selectors,omitempty"`
	States          []string            `json:"states,omitempty"`
	RiskFindings    []AgentFinding      `json:"risk_findings,omitempty"`
	EvidenceRefs    []EvidenceRef       `json:"evidence_refs,omitempty"`
	Confidence      float64             `json:"confidence,omitempty"`
	CapturedAt      time.Time           `json:"captured_at,omitempty"`
	CreatedAt       time.Time           `json:"created_at,omitempty"`
}

type PageActionInsight struct {
	ID           string        `json:"id"`
	Label        string        `json:"label,omitempty"`
	Kind         string        `json:"kind,omitempty"`
	SelectorHint string        `json:"selector_hint,omitempty"`
	TargetURL    string        `json:"target_url,omitempty"`
	FeatureRef   string        `json:"feature_ref,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
	Confidence   float64       `json:"confidence,omitempty"`
}

type MultimodalUnderstandingReport struct {
	ID                 string                      `json:"id"`
	ProjectID          string                      `json:"project_id"`
	SchemaVersion      string                      `json:"schema_version"`
	RequirementBrief   *RequirementBrief           `json:"requirement_brief,omitempty"`
	CodeSnapshots      []CodeUnderstandingSnapshot `json:"code_snapshots,omitempty"`
	PageSnapshots      []PageUnderstandingSnapshot `json:"page_snapshots,omitempty"`
	Summary            string                      `json:"summary,omitempty"`
	FeatureHypotheses  []*Feature                  `json:"feature_hypotheses,omitempty"`
	WorkflowCandidates []*WorkflowCandidate        `json:"workflow_candidates,omitempty"`
	InputFingerprints  map[string]string           `json:"input_fingerprints,omitempty"`
	SourceDigestSHA256 string                      `json:"source_digest_sha256,omitempty"`
	EvidenceRefs       []EvidenceRef               `json:"evidence_refs,omitempty"`
	SafetyReport       *SafetyReport               `json:"safety_report,omitempty"`
	Confidence         float64                     `json:"confidence,omitempty"`
	CreatedAt          time.Time                   `json:"created_at,omitempty"`
}
