package media

import (
	"context"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
)

type stubMiniMaxH3HarnessClient struct {
	createdPrompt string
	createCalls   int
	contextCalls  int
	queryCalls    int
}

type stubMiniMaxH3HarnessSubmitter struct {
	calls   int
	request MiniMaxH3GovernedSubmitRequest
}

func (s *stubMiniMaxH3HarnessSubmitter) Submit(_ context.Context, request MiniMaxH3GovernedSubmitRequest) (MiniMaxH3GovernedSubmitResult, error) {
	s.calls++
	s.request = request
	return MiniMaxH3GovernedSubmitResult{ProviderResult: &ContentGenerationTaskResult{
		Response: &ContentGenerationTaskResponse{ID: "governed_generation", Status: MiniMaxH3TaskQueued},
	}}, nil
}

func (s *stubMiniMaxH3HarnessClient) CreateH3ContextIRTask(_ context.Context, _ ContentGenerationTaskRequest) (ContentGenerationTaskResult, error) {
	s.contextCalls++
	return ContentGenerationTaskResult{Response: &ContentGenerationTaskResponse{ID: "context_1", Status: MiniMaxH3TaskQueued}}, nil
}

func (s *stubMiniMaxH3HarnessClient) CreateContentGenerationTask(_ context.Context, request ContentGenerationTaskRequest) (ContentGenerationTaskResult, error) {
	s.createCalls++
	for _, part := range request.Content {
		if part.Type == "text" {
			s.createdPrompt = part.Text
		}
	}
	return ContentGenerationTaskResult{Response: &ContentGenerationTaskResponse{ID: "generation_1", Status: MiniMaxH3TaskQueued}}, nil
}

func (s *stubMiniMaxH3HarnessClient) GetContentGenerationTask(_ context.Context, taskID string) (ContentGenerationTaskResult, error) {
	s.queryCalls++
	if taskID == "context_1" {
		return ContentGenerationTaskResult{Response: &ContentGenerationTaskResponse{
			ID: taskID, Status: MiniMaxH3TaskSucceeded,
			Output: map[string]any{"enhanced_prompt": "Enhanced safe presentation prompt", "task_type": "h3_context_ir"},
		}}, nil
	}
	return ContentGenerationTaskResult{Response: &ContentGenerationTaskResponse{
		ID: taskID, Status: MiniMaxH3TaskSucceeded,
		Output: map[string]any{"video_url": "https://cdn.example.test/h3.mp4"},
	}}, nil
}

func validMiniMaxH3HarnessIntent() GeneratedShotIntent {
	return GeneratedShotIntent{
		IntentID: "intro_1", Purpose: GeneratedShotPurposeIntro,
		Prompt: "Create a safe abstract intro.", DurationSec: 5, AspectRatio: "16:9",
		ContentPolicy: GeneratedShotContentPolicy{PresentationOnly: true, RequiresExplicitReview: true},
		FailurePolicy: GeneratedShotFailureContinue,
	}
}

func TestRunMiniMaxH3HarnessProducesStructurallyReviewedCandidate(t *testing.T) {
	client := &stubMiniMaxH3HarnessClient{}
	result, err := RunMiniMaxH3Harness(t.Context(), validMiniMaxH3HarnessIntent(), MiniMaxH3HarnessOptions{
		Client: client, OutputDir: t.TempDir(), Resolution: "768P", UseContextIR: true,
		ContextIRPollAttempts: 1, GenerationPollAttempts: 1,
		Downloader: &stubMiniMaxH3Downloader{content: []byte("original-video")}, Normalizer: &stubMiniMaxH3Normalizer{},
		AllowUnpricedOperatorSubmit: true, Now: func() time.Time { return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "awaiting_human_content_review" || result.Candidate == nil || result.StructuralReview == nil || !result.StructuralReview.StructurallyEligible {
		t.Fatalf("result = %+v", result)
	}
	if result.AutoIncludedInEditPlan || !result.RequiresContentReview || result.Resolution != "768P" {
		t.Fatalf("safety envelope = %+v", result)
	}
	if client.contextCalls != 1 || client.createCalls != 1 || client.queryCalls != 2 || client.createdPrompt != "Enhanced safe presentation prompt" {
		t.Fatalf("client=%+v", client)
	}
	if result.OriginalPromptSHA256 == "" || result.EnhancedPromptSHA256 == "" || result.OriginalPromptSHA256 == result.EnhancedPromptSHA256 {
		t.Fatalf("prompt digests = %q %q", result.OriginalPromptSHA256, result.EnhancedPromptSHA256)
	}
}

func TestRunMiniMaxH3HarnessRequiresExplicitOperatorOrProductionAdmission(t *testing.T) {
	client := &stubMiniMaxH3HarnessClient{}
	result, err := RunMiniMaxH3Harness(t.Context(), validMiniMaxH3HarnessIntent(), MiniMaxH3HarnessOptions{Client: client, OutputDir: t.TempDir()})
	if err == nil || result.ErrorClass != "admission_required" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if client.contextCalls != 0 || client.createCalls != 0 || client.queryCalls != 0 {
		t.Fatalf("provider calls must remain zero: %+v", client)
	}
}

func TestRunMiniMaxH3HarnessResumesWithoutCreatingAnotherTask(t *testing.T) {
	client := &stubMiniMaxH3HarnessClient{}
	result, err := RunMiniMaxH3Harness(t.Context(), validMiniMaxH3HarnessIntent(), MiniMaxH3HarnessOptions{
		Client: client, OutputDir: t.TempDir(), Resolution: "768P", UseContextIR: true,
		ExistingGenerationTaskID: "generation_existing", GenerationPollAttempts: 1,
		Downloader: &stubMiniMaxH3Downloader{content: []byte("original-video")}, Normalizer: &stubMiniMaxH3Normalizer{},
	})
	if err != nil || result.Status != "awaiting_human_content_review" || result.GenerationTaskID != "generation_existing" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if client.contextCalls != 0 || client.createCalls != 0 || client.queryCalls != 1 {
		t.Fatalf("resume must query only: %+v", client)
	}
}

func TestRunMiniMaxH3HarnessUsesGovernedSubmitterForProductionGeneration(t *testing.T) {
	client := &stubMiniMaxH3HarnessClient{}
	submitter := &stubMiniMaxH3HarnessSubmitter{}
	result, err := RunMiniMaxH3Harness(t.Context(), validMiniMaxH3HarnessIntent(), MiniMaxH3HarnessOptions{
		Client: client, Submitter: submitter, AdmissionScope: "project_1", IdempotencyKey: "intro_1_revision_1",
		OutputDir: t.TempDir(), Resolution: "768P", GenerationPollAttempts: 1,
		Downloader: &stubMiniMaxH3Downloader{content: []byte("original-video")}, Normalizer: &stubMiniMaxH3Normalizer{},
	})
	if err != nil || result.Status != "awaiting_human_content_review" || result.GenerationTaskID != "governed_generation" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if submitter.calls != 1 || submitter.request.Scope != "project_1" || submitter.request.IdempotencyKey != "intro_1_revision_1" {
		t.Fatalf("submitter=%+v", submitter)
	}
	if client.createCalls != 0 {
		t.Fatalf("direct create calls=%d", client.createCalls)
	}
}

func TestLoadMiniMaxH3HarnessConfigReusesGenericKeyOnlyForExplicitVideoRoute(t *testing.T) {
	values := map[string]string{
		"CASCADE_VIDEO_PROVIDER": "minimax", "CASCADE_VIDEO_MODEL": "minimax-h3",
		"MINIMAX_API_KEY": "generic-key", "MINIMAX_BASE_URL": "https://api.minimaxi.com/v1",
	}
	loaded, err := LoadMiniMaxH3HarnessConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mode != config.ArkMediaModeReal || loaded.APIKey != "generic-key" || loaded.APIKeySource != "MINIMAX_API_KEY" || !strings.HasSuffix(loaded.BaseURL, "/v1") {
		t.Fatalf("loaded = %+v", loaded)
	}
	values["CASCADE_VIDEO_PROVIDER"] = "seedance"
	if _, err := LoadMiniMaxH3HarnessConfig(func(key string) string { return values[key] }); err == nil {
		t.Fatal("non-H3 video route must not authorize generic MiniMax credential reuse")
	}
}
