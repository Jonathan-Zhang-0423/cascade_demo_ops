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
