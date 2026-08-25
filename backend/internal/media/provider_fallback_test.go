package media

import (
	"context"
	"testing"
	"time"
)

type fallbackAdapter struct {
	provider string
	result   GeneratedShotProviderExecutionResult
}

func (a fallbackAdapter) Descriptor() GeneratedShotProviderDescriptor {
	return GeneratedShotProviderDescriptor{Provider: a.provider, Model: a.provider + "-model", ProfileVersion: "profile-" + a.provider, Enabled: true}
}
func (a fallbackAdapter) Profile() GeneratedShotCapabilityProfile {
	return GeneratedShotCapabilityProfile{Provider: a.provider, ProfileVersion: "profile-" + a.provider, MinDurationSec: 4, MaxDurationSec: 15, AllowedAspectRatios: []string{"16:9"}}
}
func (a fallbackAdapter) Preflight(context.Context, GeneratedShotIntent) GeneratedShotProviderPreflight {
	return GeneratedShotProviderPreflight{Provider: a.provider, Selectable: true, CapabilityCompatible: true, Enabled: true}
}
func (a fallbackAdapter) Execute(context.Context, GeneratedShotProviderExecutionRequest) (GeneratedShotProviderExecutionResult, error) {
	return a.result, nil
}
func (a fallbackAdapter) Cancel(context.Context, string) error { return nil }

func fallbackIntent() GeneratedShotIntent {
	return GeneratedShotIntent{IntentID: "intent", Purpose: GeneratedShotPurposeIntro, Prompt: "abstract", DurationSec: 5, AspectRatio: "16:9", ContentPolicy: GeneratedShotContentPolicy{PresentationOnly: true, RequiresExplicitReview: true}, FailurePolicy: GeneratedShotFailureContinue}
}

func TestExecuteWithFallbackStopsAfterTaskCreation(t *testing.T) {
	registry := NewGeneratedShotProviderRegistry()
	if err := registry.Register(fallbackAdapter{provider: GeneratedShotProviderMiniMaxH3, result: GeneratedShotProviderExecutionResult{ProviderTaskID: "task_1", Status: "queued", FailurePolicy: GeneratedShotFailureContinue}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(fallbackAdapter{provider: GeneratedShotProviderSeedance25, result: GeneratedShotProviderExecutionResult{Status: GeneratedShotFailureContinue, FailurePolicy: GeneratedShotFailureContinue, ErrorClass: "failed_before_task"}}); err != nil {
		t.Fatal(err)
	}
	result, attempts := registry.ExecuteWithFallback(context.Background(), GeneratedShotProviderExecutionRequest{Intent: fallbackIntent(), GenerationAuthorized: true, AuthorizationRef: "approval", IdempotencyKey: "idempotency", AdmissionScope: "job", Timeout: time.Second}, GeneratedShotProviderMiniMaxH3)
	if result.ProviderTaskID != "task_1" || len(attempts) != 1 || attempts[0].FallbackReason == "" {
		t.Fatalf("task recovery invariant broken: result=%+v attempts=%+v", result, attempts)
	}
}

func TestExecuteWithFallbackSwitchesProviderOnlyBeforeTaskCreation(t *testing.T) {
	registry := NewGeneratedShotProviderRegistry()
	if err := registry.Register(fallbackAdapter{provider: GeneratedShotProviderMiniMaxH3, result: GeneratedShotProviderExecutionResult{Provider: GeneratedShotProviderMiniMaxH3, Status: GeneratedShotFailureContinue, FailurePolicy: GeneratedShotFailureContinue, ErrorClass: "timeout_before_create"}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(fallbackAdapter{provider: GeneratedShotProviderSeedance25, result: GeneratedShotProviderExecutionResult{Provider: GeneratedShotProviderSeedance25, Status: "failed", FailurePolicy: GeneratedShotFailureContinue, ErrorClass: "provider_rejected"}}); err != nil {
		t.Fatal(err)
	}
	result, attempts := registry.ExecuteWithFallback(context.Background(), GeneratedShotProviderExecutionRequest{Intent: fallbackIntent(), GenerationAuthorized: true, AuthorizationRef: "approval", IdempotencyKey: "idempotency", AdmissionScope: "job", Timeout: time.Second}, GeneratedShotProviderMiniMaxH3)
	if len(attempts) != 2 || attempts[0].Provider != GeneratedShotProviderMiniMaxH3 || attempts[1].Provider != GeneratedShotProviderSeedance25 || result.ErrorClass != "provider_rejected" {
		t.Fatalf("pre-create fallback did not reach compatible provider: result=%+v attempts=%+v", result, attempts)
	}
}

func TestOrderedCandidatesHonorsCreativeProviderAllowlist(t *testing.T) {
	registry := NewGeneratedShotProviderRegistry()
	if err := registry.Register(fallbackAdapter{provider: GeneratedShotProviderMiniMaxH3, result: GeneratedShotProviderExecutionResult{Status: GeneratedShotFailureContinue, FailurePolicy: GeneratedShotFailureContinue}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(fallbackAdapter{provider: GeneratedShotProviderSeedance25, result: GeneratedShotProviderExecutionResult{Status: GeneratedShotFailureContinue, FailurePolicy: GeneratedShotFailureContinue}}); err != nil {
		t.Fatal(err)
	}
	intent := fallbackIntent()
	intent.AllowedProviders = []string{GeneratedShotProviderMiniMaxH3}
	adapters, _ := registry.OrderedCandidates(context.Background(), intent, GeneratedShotProviderSeedance25)
	if len(adapters) != 1 || adapters[0].Descriptor().Provider != GeneratedShotProviderMiniMaxH3 {
		t.Fatalf("provider allowlist was not enforced: %+v", adapters)
	}
}
