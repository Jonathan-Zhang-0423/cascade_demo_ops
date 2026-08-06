package media

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	GeneratedShotStructuralReviewSchemaVersion = "demoops.generated_shot_structural_review.v1"
	GeneratedShotContentReviewSchemaVersion    = "demoops.generated_shot_content_review.v1"

	GeneratedShotStructuralEligible = "eligible_for_content_review"
	GeneratedShotStructuralRejected = "structurally_rejected"

	GeneratedShotContentApprovedPendingSelection = "content_approved_pending_selection"
	GeneratedShotContentRejected                 = "content_rejected"

	GeneratedShotContentDecisionApprove = "approve"
	GeneratedShotContentDecisionReject  = "reject"
)

const (
	generatedShotMaxRequestedDurationVarianceSec  = 1.0
	generatedShotMaxNormalizationDurationDriftSec = 0.25
)

type GeneratedShotReviewFinding struct {
	Code     string `json:"code"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// GeneratedShotStructuralReview is an automatic, deterministic review of
// identity, authority, integrity, and media-profile constraints. It never
// approves content or authorizes editor insertion.
type GeneratedShotStructuralReview struct {
	SchemaVersion         string                       `json:"schema_version"`
	ReviewID              string                       `json:"review_id"`
	IntentID              string                       `json:"intent_id"`
	CandidateID           string                       `json:"candidate_id"`
	Provider              string                       `json:"provider"`
	Status                string                       `json:"status"`
	StructurallyEligible  bool                         `json:"structurally_eligible"`
	ContentReviewRequired bool                         `json:"content_review_required"`
	ApprovedForDemo       bool                         `json:"approved_for_demo"`
	IncludeInDemo         bool                         `json:"include_in_demo"`
	Findings              []GeneratedShotReviewFinding `json:"findings,omitempty"`
}

// ReviewGeneratedShotCandidateStructure performs no provider, filesystem, or
// editor action. Callers must supply the already-resolved intent and candidate.
func ReviewGeneratedShotCandidateStructure(reviewID string, intent GeneratedShotIntent, candidate GeneratedShotCandidate) GeneratedShotStructuralReview {
	review := GeneratedShotStructuralReview{
		SchemaVersion: GeneratedShotStructuralReviewSchemaVersion,
		ReviewID:      strings.TrimSpace(reviewID), IntentID: intent.IntentID, CandidateID: candidate.CandidateID,
		Provider: candidate.Provider, Status: GeneratedShotStructuralRejected,
		ContentReviewRequired: true, ApprovedForDemo: false, IncludeInDemo: false,
	}
	add := func(code string, field string, message string) {
		review.Findings = append(review.Findings, GeneratedShotReviewFinding{Code: code, Field: field, Message: message, Severity: "error"})
	}
	if review.ReviewID == "" {
		add("generated_review_identity_missing", "review_id", "review_id is required")
	}
	if err := ValidateGeneratedShotIntent(intent); err != nil {
		if validation := asGeneratedShotValidationError(err); validation != nil {
			add(validation.Code, "intent."+validation.Field, validation.Message)
		} else {
			add("generated_intent_invalid", "intent", err.Error())
		}
	}
	if err := ValidateGeneratedShotCandidate(candidate); err != nil {
		if validation := asGeneratedShotCandidateValidationError(err); validation != nil {
			add(validation.Code, "candidate."+validation.Field, validation.Message)
		} else {
			add("generated_candidate_invalid", "candidate", err.Error())
		}
	}
	if strings.TrimSpace(candidate.IntentID) != strings.TrimSpace(intent.IntentID) {
		add("generated_review_intent_mismatch", "candidate.intent_id", "candidate must reference the reviewed intent")
	}
	actualDuration := candidate.NormalizedArtifact.Probe.DurationSec
	if actualDuration < 4 || actualDuration > 15 {
		add("generated_review_duration_out_of_range", "candidate.normalized_artifact.probe.duration_sec", "normalized candidate duration must remain within the common 4-15 second boundary")
	} else if math.Abs(actualDuration-float64(intent.DurationSec)) > generatedShotMaxRequestedDurationVarianceSec {
		add("generated_review_duration_mismatch", "candidate.normalized_artifact.probe.duration_sec", fmt.Sprintf("normalized duration %.3fs differs from requested duration %ds by more than %.2fs", actualDuration, intent.DurationSec, generatedShotMaxRequestedDurationVarianceSec))
	}
	originalDuration := candidate.OriginalArtifact.Probe.DurationSec
	if originalDuration <= 0 {
		add("generated_review_original_probe_missing", "candidate.original_artifact.probe.duration_sec", "original artifact must carry a positive probed duration")
	} else if math.Abs(originalDuration-actualDuration) > generatedShotMaxNormalizationDurationDriftSec {
		add("generated_review_normalization_duration_drift", "candidate.normalized_artifact.probe.duration_sec", fmt.Sprintf("normalization duration drift exceeds %.2fs", generatedShotMaxNormalizationDurationDriftSec))
	}
	if len(review.Findings) == 0 {
		review.Status = GeneratedShotStructuralEligible
		review.StructurallyEligible = true
	}
	return review
}

type GeneratedShotContentSafetyAssertions struct {
	PurposeMatchesIntent      bool `json:"purpose_matches_intent"`
	NoCapturedUIReplacement   bool `json:"no_captured_ui_replacement"`
	NoBusinessFactClaims      bool `json:"no_business_fact_claims"`
	NoUnverifiedTextOrNumbers bool `json:"no_unverified_text_or_numbers"`
	NoReferenceFactMutation   bool `json:"no_reference_fact_mutation"`
}

// GeneratedShotContentReviewDecision must be supplied by a human reviewer.
// The current contract deliberately does not accept a model self-approval.
type GeneratedShotContentReviewDecision struct {
	ReviewID           string                               `json:"review_id"`
	StructuralReviewID string                               `json:"structural_review_id"`
	CandidateID        string                               `json:"candidate_id"`
	IntentID           string                               `json:"intent_id"`
	ReviewerID         string                               `json:"reviewer_id"`
	ReviewerKind       string                               `json:"reviewer_kind"`
	ReviewedAt         time.Time                            `json:"reviewed_at"`
	Decision           string                               `json:"decision"`
	Assertions         GeneratedShotContentSafetyAssertions `json:"assertions"`
	EvidenceRefs       []string                             `json:"evidence_refs,omitempty"`
	Reason             string                               `json:"reason,omitempty"`
}

type GeneratedShotContentReview struct {
	SchemaVersion      string                               `json:"schema_version"`
	ReviewID           string                               `json:"review_id"`
	StructuralReviewID string                               `json:"structural_review_id"`
	CandidateID        string                               `json:"candidate_id"`
	IntentID           string                               `json:"intent_id"`
	ReviewerID         string                               `json:"reviewer_id"`
	ReviewerKind       string                               `json:"reviewer_kind"`
	ReviewedAt         time.Time                            `json:"reviewed_at"`
	Decision           string                               `json:"decision"`
	Status             string                               `json:"status"`
	Assertions         GeneratedShotContentSafetyAssertions `json:"assertions"`
	EvidenceRefs       []string                             `json:"evidence_refs,omitempty"`
	Reason             string                               `json:"reason,omitempty"`
	ContentApproved    bool                                 `json:"content_approved"`
	SelectionRequired  bool                                 `json:"selection_required"`
	ApprovedForDemo    bool                                 `json:"approved_for_demo"`
	IncludeInDemo      bool                                 `json:"include_in_demo"`
}

type GeneratedShotReviewValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *GeneratedShotReviewValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Field + ": " + e.Message
}

// RecordGeneratedShotContentReview records a human decision only after the
// structural gate. Approval remains pending explicit selection and cannot set
// include_in_demo.
func RecordGeneratedShotContentReview(structural GeneratedShotStructuralReview, decision GeneratedShotContentReviewDecision) (GeneratedShotContentReview, error) {
	fail := func(field string, code string, message string) (GeneratedShotContentReview, error) {
		return GeneratedShotContentReview{}, &GeneratedShotReviewValidationError{Field: field, Code: code, Message: message}
	}
	if structural.SchemaVersion != GeneratedShotStructuralReviewSchemaVersion || !structural.StructurallyEligible || structural.Status != GeneratedShotStructuralEligible || structural.ApprovedForDemo || structural.IncludeInDemo {
		return fail("structural_review", "generated_content_review_structure_ineligible", "content review requires an eligible, non-approved structural review")
	}
	if strings.TrimSpace(decision.ReviewID) == "" || strings.TrimSpace(decision.ReviewerID) == "" {
		return fail("review_id", "generated_content_review_identity_missing", "review_id and reviewer_id are required")
	}
	if decision.StructuralReviewID != structural.ReviewID || decision.CandidateID != structural.CandidateID || decision.IntentID != structural.IntentID {
		return fail("structural_review_id", "generated_content_review_binding_mismatch", "content decision must match the structural review, candidate, and intent")
	}
	if decision.ReviewerKind != "human" {
		return fail("reviewer_kind", "generated_content_review_human_required", "the current project profile requires a human content reviewer")
	}
	if decision.ReviewedAt.IsZero() {
		return fail("reviewed_at", "generated_content_review_time_missing", "reviewed_at is required")
	}
	if decision.Decision != GeneratedShotContentDecisionApprove && decision.Decision != GeneratedShotContentDecisionReject {
		return fail("decision", "generated_content_review_decision_invalid", "decision must be approve or reject")
	}
	if len(nonEmptyUniqueStrings(decision.EvidenceRefs)) == 0 {
		return fail("evidence_refs", "generated_content_review_evidence_missing", "at least one review evidence reference is required")
	}
	if decision.Decision == GeneratedShotContentDecisionApprove && !generatedShotContentAssertionsSafe(decision.Assertions) {
		return fail("assertions", "generated_content_review_assertions_unsafe", "all safety assertions must be true before content approval")
	}
	if decision.Decision == GeneratedShotContentDecisionReject && strings.TrimSpace(decision.Reason) == "" {
		return fail("reason", "generated_content_review_rejection_reason_missing", "a rejection reason is required")
	}
	review := GeneratedShotContentReview{
		SchemaVersion: GeneratedShotContentReviewSchemaVersion,
		ReviewID:      decision.ReviewID, StructuralReviewID: decision.StructuralReviewID,
		CandidateID: decision.CandidateID, IntentID: decision.IntentID,
		ReviewerID: decision.ReviewerID, ReviewerKind: decision.ReviewerKind, ReviewedAt: decision.ReviewedAt.UTC(),
		Decision: decision.Decision, Assertions: decision.Assertions,
		EvidenceRefs: nonEmptyUniqueStrings(decision.EvidenceRefs), Reason: strings.TrimSpace(decision.Reason),
		SelectionRequired: true, ApprovedForDemo: false, IncludeInDemo: false,
	}
	if decision.Decision == GeneratedShotContentDecisionApprove {
		review.Status = GeneratedShotContentApprovedPendingSelection
		review.ContentApproved = true
	} else {
		review.Status = GeneratedShotContentRejected
	}
	return review, nil
}

func generatedShotContentAssertionsSafe(assertions GeneratedShotContentSafetyAssertions) bool {
	return assertions.PurposeMatchesIntent && assertions.NoCapturedUIReplacement && assertions.NoBusinessFactClaims &&
		assertions.NoUnverifiedTextOrNumbers && assertions.NoReferenceFactMutation
}

func nonEmptyUniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func asGeneratedShotReviewValidationError(err error) *GeneratedShotReviewValidationError {
	var target *GeneratedShotReviewValidationError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
