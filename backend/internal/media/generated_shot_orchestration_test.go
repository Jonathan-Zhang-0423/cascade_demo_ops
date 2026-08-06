package media

import (
	"testing"
	"time"
)

func generatedShotOrchestrationFixture(t *testing.T) (GeneratedShotIntent, GeneratedShotCandidate, GeneratedShotCandidateSet, GeneratedShotSelection, GeneratedShotEditorApproval) {
	t.Helper()
	intent, candidate, set, selection := generatedShotApprovalFixture(t)
	approval, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, validGeneratedShotEditorApprovalDecision())
	if err != nil {
		t.Fatal(err)
	}
	return intent, candidate, set, selection, approval
}

func TestGeneratedShotOrchestrationAdvancesTypedArtifactsWithoutRuntimeAuthority(t *testing.T) {
	intent, _, set, selection, approval := generatedShotOrchestrationFixture(t)
	now := time.Date(2026, 8, 5, 18, 30, 0, 0, time.UTC)
	preflight := PreflightGeneratedShotIntent(intent, []GeneratedShotProviderCompiler{MiniMaxH3GeneratedShotCompiler{}}, nil)
	record, err := NewGeneratedShotOrchestration("orchestration_001", intent, GeneratedShotCandidateSetModeNormal, preflight, now)
	if err != nil {
		t.Fatal(err)
	}
	record, err = AdvanceGeneratedShotOrchestrationToCandidates(record, set, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	record, err = AdvanceGeneratedShotOrchestrationToSelection(record, set, selection, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	record, err = AdvanceGeneratedShotOrchestrationToEditorApproval(record, set, selection, approval, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if record.State != GeneratedShotOrchestrationEditorApprovedPendingPatch || record.Revision != 4 {
		t.Fatalf("orchestration = %+v", record)
	}
	if record.RuntimeRouteRegistered || record.ProviderCallsAllowed || record.EditorWritesAllowed || record.RendererAllowed || record.AutoApply {
		t.Fatalf("runtime authority widened: %+v", record)
	}
}

func TestGeneratedShotOrchestrationRejectsSkippedState(t *testing.T) {
	intent, _, set, selection, _ := generatedShotOrchestrationFixture(t)
	now := time.Date(2026, 8, 5, 18, 30, 0, 0, time.UTC)
	preflight := PreflightGeneratedShotIntent(intent, []GeneratedShotProviderCompiler{MiniMaxH3GeneratedShotCompiler{}}, nil)
	record, err := NewGeneratedShotOrchestration("orchestration_001", intent, GeneratedShotCandidateSetModeNormal, preflight, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AdvanceGeneratedShotOrchestrationToSelection(record, set, selection, now.Add(time.Minute)); err == nil {
		t.Fatal("expected skipped candidate-set state to be rejected")
	}
}

func TestGeneratedShotOrchestrationRejectsRuntimeAuthority(t *testing.T) {
	intent := validGeneratedShotIntent()
	now := time.Date(2026, 8, 5, 18, 30, 0, 0, time.UTC)
	preflight := PreflightGeneratedShotIntent(intent, []GeneratedShotProviderCompiler{MiniMaxH3GeneratedShotCompiler{}}, nil)
	record, err := NewGeneratedShotOrchestration("orchestration_001", intent, GeneratedShotCandidateSetModeNormal, preflight, now)
	if err != nil {
		t.Fatal(err)
	}
	record.ProviderCallsAllowed = true
	if err := ValidateGeneratedShotOrchestration(record); err == nil {
		t.Fatal("expected provider call authority to be rejected")
	}
	record.ProviderCallsAllowed = false
	record.EditorWritesAllowed = true
	if err := ValidateGeneratedShotOrchestration(record); err == nil {
		t.Fatal("expected editor write authority to be rejected")
	}
}

func TestGeneratedShotOrchestrationRejectsTamperedPreflightAndStaleTransition(t *testing.T) {
	intent, _, set, _, _ := generatedShotOrchestrationFixture(t)
	now := time.Date(2026, 8, 5, 18, 30, 0, 0, time.UTC)
	preflight := PreflightGeneratedShotIntent(intent, []GeneratedShotProviderCompiler{MiniMaxH3GeneratedShotCompiler{}}, nil)
	preflight.Providers[0].CompiledRequest.DryRunOnly = false
	if _, err := NewGeneratedShotOrchestration("orchestration_001", intent, GeneratedShotCandidateSetModeNormal, preflight, now); err == nil {
		t.Fatal("expected non-dry-run preflight to be rejected")
	}
	preflight = PreflightGeneratedShotIntent(intent, []GeneratedShotProviderCompiler{MiniMaxH3GeneratedShotCompiler{}}, nil)
	record, err := NewGeneratedShotOrchestration("orchestration_001", intent, GeneratedShotCandidateSetModeNormal, preflight, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AdvanceGeneratedShotOrchestrationToCandidates(record, set, now); err == nil {
		t.Fatal("expected stale transition timestamp to be rejected")
	}
}

func TestGeneratedShotComparisonOrchestrationRequiresBothPreflightProviders(t *testing.T) {
	intent := validGeneratedShotIntent()
	preflight := PreflightGeneratedShotIntent(intent, []GeneratedShotProviderCompiler{MiniMaxH3GeneratedShotCompiler{}}, nil)
	if _, err := NewGeneratedShotOrchestration("orchestration_ab", intent, GeneratedShotCandidateSetModeComparison, preflight, time.Now()); err == nil {
		t.Fatal("expected incomplete comparison preflight to be rejected")
	}
}

func TestGeneratedShotOrchestrationStopsWithoutGeneratedCandidate(t *testing.T) {
	intent := validGeneratedShotIntent()
	now := time.Date(2026, 8, 5, 18, 30, 0, 0, time.UTC)
	preflight := PreflightGeneratedShotIntent(intent, []GeneratedShotProviderCompiler{MiniMaxH3GeneratedShotCompiler{}}, nil)
	record, err := NewGeneratedShotOrchestration("orchestration_001", intent, GeneratedShotCandidateSetModeNormal, preflight, now)
	if err != nil {
		t.Fatal(err)
	}
	record, err = StopGeneratedShotOrchestration(record, "provider remains disabled during acceptance", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if record.State != GeneratedShotOrchestrationStoppedWithoutCandidate || record.FailurePolicy != GeneratedShotFailureContinue || record.StoppedReason == "" {
		t.Fatalf("stopped orchestration = %+v", record)
	}
}

func TestGeneratedShotOrchestrationRejectsFallbackMode(t *testing.T) {
	intent := validGeneratedShotIntent()
	preflight := PreflightGeneratedShotIntent(intent, []GeneratedShotProviderCompiler{MiniMaxH3GeneratedShotCompiler{}}, nil)
	if _, err := NewGeneratedShotOrchestration("orchestration_001", intent, "fallback", preflight, time.Now()); err == nil {
		t.Fatal("expected fallback orchestration to remain unsupported")
	}
}
