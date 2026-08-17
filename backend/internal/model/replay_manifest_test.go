package model

import (
	"testing"
	"time"
)

func TestReplayManifestValidateRequiresIdentity(t *testing.T) {
	m := validReplayManifest()
	if err := m.Validate(); err != nil {
		t.Fatalf("valid manifest must pass: %v", err)
	}

	empty := ReplayManifest{}
	if err := empty.Validate(); err == nil {
		t.Fatal("manifest with no identity must fail validation")
	}
}

func TestReplayManifestValidateRequiresHashes(t *testing.T) {
	m := validReplayManifest()
	m.BundleHashSHA256 = ""
	if err := m.Validate(); err == nil {
		t.Fatal("manifest without bundle hash must fail")
	}
	m = validReplayManifest()
	m.PolicyHashSHA256 = ""
	if err := m.Validate(); err == nil {
		t.Fatal("manifest without policy hash must fail")
	}
}

func TestReplayManifestValidateRequiresTimestamp(t *testing.T) {
	m := validReplayManifest()
	m.CreatedAt = time.Time{}
	if err := m.Validate(); err == nil {
		t.Fatal("manifest without created_at must fail")
	}
}

func TestReplayManifestStageOrderingPreserved(t *testing.T) {
	m := validReplayManifest()
	m.Stages = []ReplayManifestStage{
		{NodeID: "node_1", StageID: "stage_1", Order: 1, Status: "passed"},
		{NodeID: "node_2", StageID: "stage_2", Order: 2, Status: "failed", FailureCode: "STAGE_FAILED"},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest with ordered stages must pass: %v", err)
	}
	if m.Stages[0].Order != 1 || m.Stages[1].Order != 2 {
		t.Fatal("stage ordering must be preserved")
	}
}

func TestReplayManifestStageCarriesReplayableTargetAndEvidenceMetadata(t *testing.T) {
	m := validReplayManifest()
	m.Stages = []ReplayManifestStage{{
		NodeID: "node-1", StageID: "stage-1", Order: 1, Status: "passed",
		TargetURL:         "https://app.example/projects/{project_id}",
		ActionEvidenceIDs: []string{"e-action"}, OutcomeEvidenceIDs: []string{"e-outcome"},
		RecordingStartOffsetMS: 100, RecordingEndOffsetMS: 900,
		Viewport: &BrowserGeometryViewport{Width: 1280, Height: 720, DPR: 1},
	}}
	if err := m.Validate(); err != nil {
		t.Fatalf("replayable stage metadata should validate: %v", err)
	}
	if m.Stages[0].RecordingEndOffsetMS != 900 || m.Stages[0].Viewport.Width != 1280 {
		t.Fatalf("replay metadata was not retained: %+v", m.Stages[0])
	}
}

func TestReplayManifestStageRejectsInvalidRecordingInterval(t *testing.T) {
	m := validReplayManifest()
	m.Stages = []ReplayManifestStage{{NodeID: "node-1", StageID: "stage-1", Order: 1, Status: "passed", RecordingStartOffsetMS: 900, RecordingEndOffsetMS: 100}}
	if err := m.Validate(); err == nil {
		t.Fatal("replay manifest must reject a reversed recording interval")
	}
}

func TestReplayManifestRejectsIncompleteSelectorRepairAudit(t *testing.T) {
	m := validReplayManifest()
	m.Stages = []ReplayManifestStage{{
		NodeID: "node-1", StageID: "stage-1", Order: 1, Status: "passed",
		SelectorRepairs: []ReplayManifestSelectorRepair{{OriginalSelector: "[data-testid=old]", CandidateSelector: "[data-testid=new]"}},
	}}
	if err := m.Validate(); err == nil {
		t.Fatal("selector repair without evidence and match count must fail")
	}
}

func validReplayManifest() ReplayManifest {
	return ReplayManifest{
		SchemaVersion:    ReplayManifestSchemaVersion,
		ManifestID:       "manifest_test_001",
		RunID:            "run_test_001",
		PackageID:        "pkg_test_001",
		BundleHashSHA256: "deadbeef",
		PolicyHashSHA256: "cafebabe",
		Status:           "failed",
		FinalDecision:    ValidationDecisionStopAndReport,
		CreatedAt:        time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
	}
}
