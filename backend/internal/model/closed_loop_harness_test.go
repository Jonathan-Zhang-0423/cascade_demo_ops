package model

import "testing"

func TestPublicNarrativeFactRejectsInternalSyntax(t *testing.T) {
	for _, caption := range []string{"<head> leaked", `{"expected_outcome":"ready"}`, "selector=.primary", "source=browser"} {
		if ValidatePublicCaption(caption) == nil {
			t.Fatalf("internal caption was accepted: %q", caption)
		}
	}
	valid := PublicNarrativeFact{SchemaVersion: PublicNarrativeFactSchemaVersion, FactID: "fact_1", Chapter: "creation", ApprovedCaptionVariants: []string{"创建一个全新项目"}, VisibleEvidenceRefs: []string{"frame_1"}, SourceKind: "visible_ui"}
	if err := ValidatePublicNarrativeFact(valid); err != nil {
		t.Fatalf("valid public fact rejected: %v", err)
	}
}

func TestMediaCoverageRequiresSevenOrderedBusinessChapters(t *testing.T) {
	chapters := []MediaChapterCoverage{}
	for _, chapter := range RequiredDemoChapters() {
		chapters = append(chapters, MediaChapterCoverage{Chapter: chapter, Covered: true, ArtifactRefs: []string{"segment_" + chapter}, Rerecordable: chapter != "login" && chapter != "creation" && chapter != "prompt_input" && chapter != "submission"})
	}
	report := MediaCoverageReport{SchemaVersion: MediaCoverageReportSchemaVersion, Chapters: chapters, Complete: true}
	if err := ValidateMediaCoverageReport(report); err != nil {
		t.Fatalf("complete coverage rejected: %v", err)
	}
	report.Chapters[1].Covered = false
	if err := ValidateMediaCoverageReport(report); err == nil {
		t.Fatal("incomplete creation coverage was accepted")
	}
}
