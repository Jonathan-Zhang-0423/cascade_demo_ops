package model

import (
	"reflect"
	"strings"
	"time"
)

const ProductSourceBindingAssessmentSchemaVersion = "demoops.product_source_binding_assessment.v1"

type ProductSourceBindingStatus string

const (
	ProductSourceBindingMatched       ProductSourceBindingStatus = "matched"
	ProductSourceBindingMismatched    ProductSourceBindingStatus = "mismatched"
	ProductSourceBindingUnverified    ProductSourceBindingStatus = "unverified"
	ProductSourceBindingNotApplicable ProductSourceBindingStatus = "not_applicable"
)

type ProductSourceEffectiveMode string

const (
	ProductSourceModeMixed    ProductSourceEffectiveMode = "mixed"
	ProductSourceModePageOnly ProductSourceEffectiveMode = "page_only"
	ProductSourceModeBlocked  ProductSourceEffectiveMode = "blocked"
)

// ProductIdentitySignal contains comparison-safe hashes, never raw source paths or identities.
type ProductIdentitySignal struct {
	Kind         string        `json:"kind"`
	Strength     string        `json:"strength"`
	ValueSHA256  string        `json:"value_sha256"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type ProductSourceBindingItem struct {
	SourceRefID      string                     `json:"source_ref_id"`
	Status           ProductSourceBindingStatus `json:"status"`
	Signals          []ProductIdentitySignal    `json:"signals,omitempty"`
	MatchedKinds     []string                   `json:"matched_kinds,omitempty"`
	ConflictingKinds []string                   `json:"conflicting_kinds,omitempty"`
}

type ProductSourceBindingAssessment struct {
	SchemaVersion   string                     `json:"schema_version"`
	Status          ProductSourceBindingStatus `json:"status"`
	EffectiveMode   ProductSourceEffectiveMode `json:"effective_mode"`
	AssessmentHash  string                     `json:"assessment_hash"`
	InputHashSHA256 string                     `json:"input_hash_sha256"`
	ProductSignals  []ProductIdentitySignal    `json:"product_signals,omitempty"`
	Sources         []ProductSourceBindingItem `json:"sources,omitempty"`
	Decision        string                     `json:"decision,omitempty"`
	AssessedAt      time.Time                  `json:"assessed_at"`
}

type SourceBindingSummary struct {
	SchemaVersion  string                     `json:"schema_version"`
	Status         ProductSourceBindingStatus `json:"status"`
	EffectiveMode  ProductSourceEffectiveMode `json:"effective_mode"`
	AssessmentHash string                     `json:"assessment_hash"`
	SourceCount    int                        `json:"source_count"`
}

type ProductSourceMismatchError struct {
	Assessment *ProductSourceBindingAssessment
}

func (e *ProductSourceMismatchError) Error() string { return "网页与源码来源不匹配" }

type SourceEvidenceLeakageError struct{}

func (e *SourceEvidenceLeakageError) Error() string {
	return "page-only execution package contains source-derived evidence"
}

func IsSourceEvidenceKind(kind EvidenceKind) bool {
	return kind == EvidenceKindSourceCode || kind == EvidenceKindCodeSnapshot || kind == EvidenceKindRepoSnapshot
}

func IsSourceDerivedProvenance(source string) bool {
	source = strings.ToLower(strings.TrimSpace(source))
	return strings.Contains(source, "code_reader") || strings.Contains(source, "source_code") ||
		strings.Contains(source, "code_evidence") || strings.Contains(source, "repo_snapshot")
}

// ClientPackageContainsSourceDerivedExecutionEvidence is shared by the App
// preflight and server intake so page-only packages cannot exploit drift.
func ClientPackageContainsSourceDerivedExecutionEvidence(pkg *ClientExecutionPackage) bool {
	return containsSourceDerivedExecutionValue(reflect.ValueOf(pkg))
}

func containsSourceDerivedExecutionValue(value reflect.Value) bool {
	if !value.IsValid() {
		return false
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		return !value.IsNil() && containsSourceDerivedExecutionValue(value.Elem())
	}
	if value.CanInterface() {
		switch typed := value.Interface().(type) {
		case EvidenceRef:
			return IsSourceEvidenceKind(typed.Kind)
		case SelectorCandidate:
			if IsSourceDerivedProvenance(typed.Source) {
				return true
			}
		case ActionTarget:
			if IsSourceDerivedProvenance(typed.Source) {
				return true
			}
		case BrowserAgentComponentTarget:
			if IsSourceDerivedProvenance(typed.Source) {
				return true
			}
		case BrowserAgentRouteCandidate:
			if IsSourceDerivedProvenance(typed.Source) {
				return true
			}
		case ComponentSummary:
			if typed.FilePathHashSHA256 != "" {
				return true
			}
		case DataModelSummary:
			if typed.SourcePathHashSHA256 != "" || typed.ID != "" {
				return true
			}
		case StageApprovalStage:
			if len(typed.APIRefs) > 0 || len(typed.DataModelRefs) > 0 {
				return true
			}
		case SourceTreeDigest:
			if typed.RepositoryID != "" || typed.RootDigestSHA256 != "" || len(typed.PathDigests) > 0 {
				return true
			}
		}
	}
	switch value.Kind() {
	case reflect.Struct:
		if value.Type().PkgPath() != reflect.TypeOf(ClientExecutionPackage{}).PkgPath() {
			return false
		}
		for i := 0; i < value.NumField(); i++ {
			if containsSourceDerivedExecutionValue(value.Field(i)) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if containsSourceDerivedExecutionValue(value.Index(i)) {
				return true
			}
		}
	}
	return false
}
