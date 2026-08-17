package media

import (
	"context"
	"errors"
	"testing"
)

func TestGeneratedShotProviderRegistrySelectsOnlyCompatibleEnabledAdapter(t *testing.T) {
	disabled, err := NewMiniMaxH3ProviderAdapter(MiniMaxH3ProviderAdapterOptions{Enabled: false, DisabledReason: "disabled for test"})
	if err != nil {
		t.Fatal(err)
	}
	registry := NewGeneratedShotProviderRegistry()
	if err := registry.Register(disabled); err != nil {
		t.Fatal(err)
	}
	intent := providerAdapterIntent()
	_, preflight, err := registry.Select(context.Background(), intent, GeneratedShotProviderMiniMaxH3)
	if err == nil || preflight.FailureCode != "provider_disabled" {
		t.Fatalf("disabled adapter became selectable: preflight=%+v err=%v", preflight, err)
	}
	if len(registry.Descriptors()) != 1 || registry.Descriptors()[0].Enabled {
		t.Fatalf("unexpected descriptors: %+v", registry.Descriptors())
	}
}

func TestMiniMaxH3ProviderAdapterRequiresPersistedAuthorizationBeforeAnyCall(t *testing.T) {
	client := &authorizationGuardH3Client{}
	submitter := &authorizationGuardH3Submitter{}
	adapter, err := NewMiniMaxH3ProviderAdapter(MiniMaxH3ProviderAdapterOptions{
		Enabled: true, Client: client, Canceller: client, Submitter: submitter,
		Downloader: authorizationGuardH3Downloader{}, Normalizer: authorizationGuardH3Normalizer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Execute(context.Background(), GeneratedShotProviderExecutionRequest{
		Intent: providerAdapterIntent(), GenerationAuthorized: false,
		IdempotencyKey: "job_1:intro_1", AdmissionScope: "job_1", OutputDir: t.TempDir(),
	})
	if err == nil || result.ErrorClass != "generation_authorization_required" {
		t.Fatalf("expected authorization gate, got result=%+v err=%v", result, err)
	}
	if client.calls != 0 || submitter.calls != 0 {
		t.Fatalf("provider was called without authorization: client=%d submitter=%d", client.calls, submitter.calls)
	}
}

func TestMiniMaxH3ProviderAdapterRequiresGovernedSubmitterWhenEnabled(t *testing.T) {
	_, err := NewMiniMaxH3ProviderAdapter(MiniMaxH3ProviderAdapterOptions{Enabled: true, Client: &authorizationGuardH3Client{}})
	if err == nil {
		t.Fatal("expected governed submitter requirement")
	}
}

func providerAdapterIntent() GeneratedShotIntent {
	return GeneratedShotIntent{
		IntentID: "intro_1", Purpose: GeneratedShotPurposeIntro, Prompt: "Abstract product intro without text or UI.",
		DurationSec: 5, AspectRatio: "16:9", FailurePolicy: GeneratedShotFailureContinue,
		ContentPolicy: GeneratedShotContentPolicy{PresentationOnly: true, RequiresExplicitReview: true},
	}
}

type authorizationGuardH3Client struct{ calls int }

func (c *authorizationGuardH3Client) CreateH3ContextIRTask(context.Context, ContentGenerationTaskRequest) (ContentGenerationTaskResult, error) {
	c.calls++
	return ContentGenerationTaskResult{}, errors.New("unexpected call")
}

func (c *authorizationGuardH3Client) CreateContentGenerationTask(context.Context, ContentGenerationTaskRequest) (ContentGenerationTaskResult, error) {
	c.calls++
	return ContentGenerationTaskResult{}, errors.New("unexpected call")
}

func (c *authorizationGuardH3Client) GetContentGenerationTask(context.Context, string) (ContentGenerationTaskResult, error) {
	c.calls++
	return ContentGenerationTaskResult{}, errors.New("unexpected call")
}

func (c *authorizationGuardH3Client) CancelOrDeleteContentGenerationTask(context.Context, string) (MiniMaxH3TaskMutationResult, error) {
	c.calls++
	return MiniMaxH3TaskMutationResult{}, errors.New("unexpected call")
}

type authorizationGuardH3Submitter struct{ calls int }

func (s *authorizationGuardH3Submitter) Submit(context.Context, MiniMaxH3GovernedSubmitRequest) (MiniMaxH3GovernedSubmitResult, error) {
	s.calls++
	return MiniMaxH3GovernedSubmitResult{}, errors.New("unexpected call")
}

type authorizationGuardH3Downloader struct{}

func (authorizationGuardH3Downloader) Download(context.Context, string, string, int64) (MiniMaxH3DownloadedFile, error) {
	return MiniMaxH3DownloadedFile{}, errors.New("unexpected call")
}

type authorizationGuardH3Normalizer struct{}

func (authorizationGuardH3Normalizer) Normalize(context.Context, string, string) (MiniMaxH3MediaProbe, MiniMaxH3MediaProbe, error) {
	return MiniMaxH3MediaProbe{}, MiniMaxH3MediaProbe{}, errors.New("unexpected call")
}
