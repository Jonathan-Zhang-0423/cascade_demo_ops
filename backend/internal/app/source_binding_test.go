package app

import (
	"context"
	"errors"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/store"
)

func TestBridgeMapsProductSourceMismatchStructurally(t *testing.T) {
	err := &model.ProductSourceMismatchError{Assessment: &model.ProductSourceBindingAssessment{AssessmentHash: "hash-only"}}
	info := bridgeErrorInfo(err)
	if info.Code != "product_source_mismatch" || info.Message != "网页与源码来源不匹配" || info.Retryable || len(info.Details) != 2 {
		t.Fatalf("unexpected structured mismatch mapping: %+v", info)
	}
	if info.Details[0].Field != "project.product_url" || info.Details[0].Reason != "conflicts_with_source_identity" || info.Details[1].Field != "project.sources" {
		t.Fatalf("mismatch details lost stable fields/reason: %+v", info.Details)
	}
}

func TestDecideSourceBindingRejectsStaleHashAndRequiresIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{DataRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic}, states)
	if err != nil {
		t.Fatal(err)
	}
	state := sourceBindingDecisionState("project-binding", "assessment-current")
	if err := states.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	_, err = service.DecideSourceBinding(ctx, state.ProjectID, SourceBindingDecisionRequest{Decision: "continue_page_only", AssessmentHash: "assessment-old", IdempotencyKey: "decision-1"})
	var stale *SourceBindingStaleError
	if !errors.As(err, &stale) || bridgeErrorCode(err) != "source_binding_stale" {
		t.Fatalf("expected typed stale assessment, got %T code=%s err=%v", err, bridgeErrorCode(err), err)
	}
	_, err = service.DecideSourceBinding(ctx, state.ProjectID, SourceBindingDecisionRequest{Decision: "continue_page_only", AssessmentHash: "assessment-current"})
	if err == nil || err.Error() != "idempotency_key is required" {
		t.Fatalf("missing idempotency key must be rejected, got %v", err)
	}
}

func TestDecideSourceBindingIsIdempotentAfterPageOnlyDecision(t *testing.T) {
	ctx := context.Background()
	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{DataRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic}, states)
	if err != nil {
		t.Fatal(err)
	}
	state := sourceBindingDecisionState("project-page-only", "assessment-current")
	state.SourceBinding.Decision = "continue_page_only"
	state.SourceBinding.EffectiveMode = model.ProductSourceModePageOnly
	if err := states.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	got, err := service.DecideSourceBinding(ctx, state.ProjectID, SourceBindingDecisionRequest{Decision: "continue_page_only", AssessmentHash: "assessment-current", IdempotencyKey: "decision-repeat"})
	if err != nil || got != state {
		t.Fatalf("repeated page-only decision must return current state without rerun: got=%p want=%p err=%v", got, state, err)
	}
}

func TestDecideSourceBindingRejectsMixedConfirmationForDetectedMismatch(t *testing.T) {
	ctx := context.Background()
	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{DataRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic}, states)
	if err != nil {
		t.Fatal(err)
	}
	state := sourceBindingDecisionState("project-mismatch-confirmation", "assessment-current")
	if err := states.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	_, err = service.DecideSourceBinding(ctx, state.ProjectID, SourceBindingDecisionRequest{Decision: "confirm_mixed", AssessmentHash: "assessment-current", IdempotencyKey: "decision-confirm-mismatch"})
	if err == nil || err.Error() != "confirm_mixed is allowed only for an unverified source binding without a detected mismatch" {
		t.Fatalf("detected mismatch confirmation was not rejected: %v", err)
	}
}

func TestDecideSourceBindingIsIdempotentAfterMixedConfirmation(t *testing.T) {
	ctx := context.Background()
	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{DataRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic}, states)
	if err != nil {
		t.Fatal(err)
	}
	state := sourceBindingDecisionState("project-confirmed", "assessment-current")
	state.SourceBinding.Status = model.ProductSourceBindingConfirmed
	state.SourceBinding.Decision = "confirm_mixed"
	state.SourceBinding.EffectiveMode = model.ProductSourceModeMixed
	if err := states.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	got, err := service.DecideSourceBinding(ctx, state.ProjectID, SourceBindingDecisionRequest{Decision: "confirm_mixed", AssessmentHash: "assessment-current", IdempotencyKey: "decision-confirm-repeat"})
	if err != nil || got != state {
		t.Fatalf("repeated mixed confirmation must return current state without rerun: got=%p want=%p err=%v", got, state, err)
	}
}

func TestDecideSourceBindingPersistsMixedConfirmationWithoutReanalysis(t *testing.T) {
	ctx := context.Background()
	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{DataRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic}, states)
	if err != nil {
		t.Fatal(err)
	}
	state := sourceBindingDecisionState("project-confirm-without-rerun", "assessment-current")
	state.SourceBinding.Status = model.ProductSourceBindingUnverified
	state.SourceBinding.EffectiveMode = model.ProductSourceModePageOnly
	state.ProjectIntelligence = &model.ProjectIntelligencePack{ProjectID: state.ProjectID, SourceBinding: state.SourceBinding}
	if err := states.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	got, err := service.DecideSourceBinding(ctx, state.ProjectID, SourceBindingDecisionRequest{Decision: "confirm_mixed", AssessmentHash: "assessment-current", IdempotencyKey: "decision-confirm-once"})
	if err != nil {
		t.Fatal(err)
	}
	if got != state || got.SourceBinding.Status != model.ProductSourceBindingConfirmed || got.SourceBinding.EffectiveMode != model.ProductSourceModeMixed || got.ProjectContext.SourceBinding != got.SourceBinding || got.ProjectIntelligence.SourceBinding != got.SourceBinding {
		t.Fatalf("mixed confirmation did not persist the same audited state: %+v", got)
	}
	persisted, err := states.Load(ctx, state.ProjectID)
	if err != nil || persisted.SourceBinding.Status != model.ProductSourceBindingConfirmed || persisted.SourceBinding.Decision != "confirm_mixed" {
		t.Fatalf("mixed confirmation was not saved: state=%+v err=%v", persisted, err)
	}
}

func sourceBindingDecisionState(projectID, assessmentHash string) *orchestrator.CascadeState {
	assessment := &model.ProductSourceBindingAssessment{
		SchemaVersion: model.ProductSourceBindingAssessmentSchemaVersion,
		Status:        model.ProductSourceBindingMismatched, EffectiveMode: model.ProductSourceModeBlocked,
		AssessmentHash: assessmentHash,
	}
	return &orchestrator.CascadeState{
		ProjectID: projectID, SourceBinding: assessment,
		ProjectContext: &model.ProjectContext{ID: projectID, Mode: model.AppModeDesktop, ProductURL: "https://product.example", SourceBinding: assessment, Inputs: &model.ProjectInputBundle{}},
	}
}
