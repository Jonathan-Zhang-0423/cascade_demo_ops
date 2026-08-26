package model

import (
	"errors"
	"fmt"
	"strings"
)

const (
	RepairDirectiveSchemaVersion     = "demoops.repair_directive.v1"
	PublicNarrativeFactSchemaVersion = "demoops.public_narrative_fact.v1"
	MediaCoverageReportSchemaVersion = "demoops.media_coverage_report.v1"
)

// RepairDirective is the only cross-module repair instruction used by the
// closed-loop harness. It names evidence, a bounded attempt and the phase to
// resume; it deliberately does not carry executable selectors or credentials.
type RepairDirective struct {
	SchemaVersion string   `json:"schema_version"`
	SourceModule  string   `json:"source_module"`
	FailureClass  string   `json:"failure_class"`
	TargetModule  string   `json:"target_module"`
	Action        string   `json:"action"`
	ArtifactRefs  []string `json:"artifact_refs,omitempty"`
	Attempt       int      `json:"attempt"`
	MaxAttempts   int      `json:"max_attempts"`
	ResumePhase   string   `json:"resume_phase"`
}

// PublicNarrativeFact is the sole text boundary exposed to the Director. The
// approved variants must be authored from the user goal, visible UI, or a
// verified product fact. Internal execution fields are never valid variants.
type PublicNarrativeFact struct {
	SchemaVersion           string   `json:"schema_version"`
	FactID                  string   `json:"fact_id"`
	Chapter                 string   `json:"chapter"`
	ApprovedCaptionVariants []string `json:"approved_caption_variants"`
	VisibleEvidenceRefs     []string `json:"visible_evidence_refs"`
	SourceKind              string   `json:"source_kind"`
}

type MediaChapterCoverage struct {
	Chapter       string   `json:"chapter"`
	Covered       bool     `json:"covered"`
	ArtifactRefs  []string `json:"artifact_refs,omitempty"`
	QualityIssues []string `json:"quality_issues,omitempty"`
	Rerecordable  bool     `json:"rerecordable"`
	RerecordCount int      `json:"rerecord_count,omitempty"`
}

type MediaCoverageReport struct {
	SchemaVersion string                 `json:"schema_version"`
	Chapters      []MediaChapterCoverage `json:"chapters"`
	Rerecordable  []string               `json:"rerecordable_chapters,omitempty"`
	NonReplayable []string               `json:"non_replayable_chapters,omitempty"`
	QualityIssues []string               `json:"quality_issues,omitempty"`
	Complete      bool                   `json:"complete"`
}

var requiredDemoChapters = []string{"login", "creation", "prompt_input", "submission", "build_wait", "result_reveal", "interaction"}

func RequiredDemoChapters() []string {
	return append([]string{}, requiredDemoChapters...)
}

func ValidateRepairDirective(value RepairDirective) error {
	if value.SchemaVersion != RepairDirectiveSchemaVersion || strings.TrimSpace(value.SourceModule) == "" || strings.TrimSpace(value.FailureClass) == "" || strings.TrimSpace(value.TargetModule) == "" || strings.TrimSpace(value.Action) == "" || strings.TrimSpace(value.ResumePhase) == "" {
		return errors.New("repair directive identity and routing fields are required")
	}
	if value.Attempt < 1 || value.MaxAttempts < value.Attempt || value.MaxAttempts > 3 {
		return errors.New("repair directive attempt must be bounded to at most three")
	}
	return nil
}

func ValidatePublicNarrativeFact(value PublicNarrativeFact) error {
	if value.SchemaVersion != PublicNarrativeFactSchemaVersion || strings.TrimSpace(value.FactID) == "" || strings.TrimSpace(value.Chapter) == "" || len(value.ApprovedCaptionVariants) == 0 || len(value.VisibleEvidenceRefs) == 0 {
		return errors.New("public narrative fact identity, chapter, captions, and visible evidence are required")
	}
	if value.SourceKind != "user_goal" && value.SourceKind != "visible_ui" && value.SourceKind != "verified_product_fact" {
		return errors.New("public narrative fact source_kind is unsupported")
	}
	for _, caption := range value.ApprovedCaptionVariants {
		if err := ValidatePublicCaption(caption); err != nil {
			return fmt.Errorf("public narrative fact %s: %w", value.FactID, err)
		}
	}
	return nil
}

// ValidatePublicCaption is intentionally allowlist-shaped: captions must be
// short display copy and cannot contain syntax used by HTML, JSON, selectors,
// schema fields or execution-log assertions.
func ValidatePublicCaption(value string) error {
	caption := strings.TrimSpace(value)
	if caption == "" || len([]rune(caption)) > 80 {
		return errors.New("caption must contain 1-80 visible characters")
	}
	lower := strings.ToLower(caption)
	for _, token := range []string{"<", ">", "{", "}", "=", "selector", "schema", "observed_state", "expected_outcome", "source=", "assertion:", "node_id", "step_id", "request json", "dom dump", "html"} {
		if strings.Contains(lower, token) {
			return fmt.Errorf("caption contains prohibited internal token %q", token)
		}
	}
	return nil
}

func ValidateMediaCoverageReport(value MediaCoverageReport) error {
	if value.SchemaVersion != MediaCoverageReportSchemaVersion {
		return errors.New("unsupported media coverage report schema")
	}
	byChapter := make(map[string]MediaChapterCoverage, len(value.Chapters))
	for _, chapter := range value.Chapters {
		if _, exists := byChapter[chapter.Chapter]; exists {
			return fmt.Errorf("duplicate media chapter %s", chapter.Chapter)
		}
		if chapter.RerecordCount < 0 || chapter.RerecordCount > 2 {
			return fmt.Errorf("media chapter %s exceeds its rerecord budget", chapter.Chapter)
		}
		byChapter[chapter.Chapter] = chapter
	}
	complete := true
	for _, chapter := range requiredDemoChapters {
		entry, ok := byChapter[chapter]
		if !ok || !entry.Covered || len(entry.ArtifactRefs) == 0 || len(entry.QualityIssues) > 0 {
			complete = false
		}
	}
	if value.Complete != complete {
		return errors.New("media coverage complete flag does not match required chapter evidence")
	}
	return nil
}
