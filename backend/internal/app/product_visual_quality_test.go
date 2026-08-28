package app

import (
	"encoding/json"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/experiment"
)

func TestSelectProductVisualQualityScreenshotPrefersTerminalPreview(t *testing.T) {
	items := []CloudDeliverableDownloadResult{
		{ArtifactID: "surface_ready_after", MimeType: "image/png"},
		{ArtifactID: "terminal_scenes_after", MimeType: "image/png"},
		{ArtifactID: "terminal_scenes_before", MimeType: "image/png"},
	}
	got, ok := selectProductVisualQualityScreenshot(items)
	if !ok || got.ArtifactID != "terminal_scenes_after" {
		t.Fatalf("terminal product state was not selected: ok=%v got=%+v", ok, got)
	}
}

func TestProductVisualQualityDraftAcceptsNestedProviderShape(t *testing.T) {
	raw := []byte(`{"answer":"{\"pass\":\"failed\",\"confidence\":\"96%\",\"summary\":\"The preview uses a generic light theme.\",\"visible_evidence\":\"A beige board is visible.\",\"findings\":[\"Requested neon palette is absent.\"],\"failed_requirements\":\"deep blue neon\"}"}`)
	var draft productVisualQualityDraft
	if err := json.Unmarshal(raw, &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Pass || draft.Confidence != .96 || len(draft.VisibleEvidence) != 1 || len(draft.FailedRequirements) != 1 {
		t.Fatalf("nested visual review was not normalized: %+v", draft)
	}
}

func TestProductVisualRequirementSummaryKeepsFrozenPublicVisualRequirements(t *testing.T) {
	spec := experiment.ProductSpec{
		VisualDirection:      experiment.VisualDirection{Theme: "deep blue neon", Palette: []string{"navy", "cyan"}},
		ObservableAcceptance: []experiment.AcceptanceCriterion{{Statement: "The product uses a deep blue gradient.", EvidenceKinds: []string{"visual"}, Required: true}, {Statement: "Keyboard works.", EvidenceKinds: []string{"dom"}, Required: true}},
		ForbiddenOutcomes:    []string{"Do not show a generic default theme."},
	}
	got := productVisualRequirementSummary(spec)
	for _, required := range []string{"deep blue neon", "navy, cyan", "deep blue gradient", "generic default theme"} {
		if !strings.Contains(got, required) {
			t.Fatalf("visual summary lost %q: %s", required, got)
		}
	}
	if strings.Contains(got, "Keyboard works") {
		t.Fatalf("non-visual criterion leaked into the visual-only Gate: %s", got)
	}
}

func TestNormalizeProductVisualQualityDraftRejectsLowConfidenceAndFailedRequirements(t *testing.T) {
	for _, draft := range []productVisualQualityDraft{
		{Pass: true, Confidence: .7, Summary: "uncertain"},
		{Pass: true, Confidence: .95, Summary: "mismatch", FailedRequirements: []string{"palette"}},
	} {
		report, err := normalizeProductVisualQualityDraft(draft, "actual-preview")
		if err != nil || report.Pass {
			t.Fatalf("unproven product visuals passed: report=%+v err=%v", report, err)
		}
	}
}
