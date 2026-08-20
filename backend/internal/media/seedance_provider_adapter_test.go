package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type stubSeedanceVideoClient struct {
	createCalls int
	pollCalls   int
	lastRequest ContentGenerationTaskRequest
}

func (c *stubSeedanceVideoClient) CreateContentGenerationTask(_ context.Context, request ContentGenerationTaskRequest) (ContentGenerationTaskResult, error) {
	c.createCalls++
	c.lastRequest = request
	return ContentGenerationTaskResult{Response: &ContentGenerationTaskResponse{ID: "seedance_task_1", Status: "queued"}}, nil
}

func TestSeedance25ProviderAdapterPinsOfficialModelAndEntersCommonReview(t *testing.T) {
	client := &stubSeedanceVideoClient{}
	adapter, err := NewSeedance25ProviderAdapter(Seedance25ProviderAdapterOptions{
		Enabled: true, Client: client, Downloader: stubSeedanceDownloader{}, Normalizer: stubSeedanceNormalizer{}, PollInterval: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	outputDir := t.TempDir()
	result, err := adapter.Execute(context.Background(), GeneratedShotProviderExecutionRequest{
		Intent: validGeneratedShotIntent(), GenerationAuthorized: true, AuthorizationRef: "approval://seedance-2.5/1",
		IdempotencyKey: "seedance-2.5-idempotency", AdmissionScope: "final-film:seedance-2.5", OutputDir: outputDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.lastRequest.Model != Seedance25ServerModel || client.lastRequest.GenerateAudio || !client.lastRequest.ReturnLastFrame {
		t.Fatalf("unexpected Seedance 2.5 request: %+v", client.lastRequest)
	}
	if result.Provider != GeneratedShotProviderSeedance25 || result.Candidate == nil || result.Candidate.Provider != GeneratedShotProviderSeedance25 || !result.StructuralReview.StructurallyEligible {
		t.Fatalf("Seedance 2.5 did not enter common review contract: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(outputDir, GeneratedShotProviderSeedance25, "seedance_task_1", "normalized.mp4")); err != nil {
		t.Fatal(err)
	}
}

func TestSeedance25ProviderAdapterResumesWithoutCreatingAnotherTask(t *testing.T) {
	client := &stubSeedanceVideoClient{}
	adapter, err := NewSeedance25ProviderAdapter(Seedance25ProviderAdapterOptions{
		Enabled: true, Client: client, Downloader: stubSeedanceDownloader{}, Normalizer: stubSeedanceNormalizer{}, PollInterval: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Execute(context.Background(), GeneratedShotProviderExecutionRequest{
		Intent: validGeneratedShotIntent(), GenerationAuthorized: true, AuthorizationRef: "approval://resume/1",
		IdempotencyKey: "resume-idempotency", AdmissionScope: "final-film:resume", OutputDir: t.TempDir(),
		ResumeProviderTaskID: "seedance_task_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.createCalls != 0 || client.pollCalls != 1 || result.ProviderTaskID != "seedance_task_1" {
		t.Fatalf("resume should query exactly once without creating: client=%+v result=%+v", client, result)
	}
}

func TestSeedance25ProviderAdapterAdmissionResumesTimedOutTaskWithoutSecondCreate(t *testing.T) {
	client := &seedancePendingThenSucceededClient{}
	gate, err := NewSeedance25AdmissionGate(Seedance25AdmissionPolicy{MaxConcurrent: 1, Window: time.Minute, MaxRequestsPerWindow: 1, IdempotencyTTL: time.Hour}, nil)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewSeedance25ProviderAdapter(Seedance25ProviderAdapterOptions{Enabled: true, Client: client, Downloader: stubSeedanceDownloader{}, Normalizer: stubSeedanceNormalizer{}, PollAttempts: 1, PollInterval: time.Nanosecond, Admission: gate})
	if err != nil {
		t.Fatal(err)
	}
	request := GeneratedShotProviderExecutionRequest{Intent: validGeneratedShotIntent(), GenerationAuthorized: true, AuthorizationRef: "approval://admission/1", IdempotencyKey: "admission-key", AdmissionScope: "final-film:admission", OutputDir: t.TempDir()}
	if _, err := adapter.Execute(context.Background(), request); err == nil {
		t.Fatal("first call should stop while provider task is pending")
	}
	if client.createCalls != 1 || gate.Snapshot().Concurrent != 1 {
		t.Fatalf("pending remote task was not reserved: client=%+v admission=%+v", client, gate.Snapshot())
	}
	client.succeed = true
	result, err := adapter.Execute(context.Background(), request)
	if err != nil || result.ProviderTaskID != "seedance_task_pending" || client.createCalls != 1 {
		t.Fatalf("same idempotency key created a duplicate task: result=%+v err=%v client=%+v", result, err, client)
	}
	if gate.Snapshot().Concurrent != 0 {
		t.Fatalf("terminal provider task did not release concurrency: %+v", gate.Snapshot())
	}
}

func (c *stubSeedanceVideoClient) GetContentGenerationTask(context.Context, string) (ContentGenerationTaskResult, error) {
	c.pollCalls++
	return ContentGenerationTaskResult{Response: &ContentGenerationTaskResponse{ID: "seedance_task_1", Status: "succeeded", Output: map[string]any{"content": map[string]any{"video_url": "https://cdn.example.test/seedance.mp4"}}}}, nil
}

type stubSeedanceDownloader struct{}

func (stubSeedanceDownloader) Download(_ context.Context, _ string, destination string, _ int64) (MiniMaxH3DownloadedFile, error) {
	if err := os.WriteFile(destination, []byte("seedance-original"), 0o600); err != nil {
		return MiniMaxH3DownloadedFile{}, err
	}
	hash, size, err := miniMaxH3FileDigest(destination)
	return MiniMaxH3DownloadedFile{Path: destination, MimeType: "video/mp4", SHA256: hash, SizeBytes: size}, err
}

type stubSeedanceNormalizer struct{}

func (stubSeedanceNormalizer) Normalize(_ context.Context, _, destination string) (MiniMaxH3MediaProbe, MiniMaxH3MediaProbe, error) {
	if err := os.WriteFile(destination, []byte("seedance-normalized"), 0o600); err != nil {
		return MiniMaxH3MediaProbe{}, MiniMaxH3MediaProbe{}, err
	}
	original := MiniMaxH3MediaProbe{Format: "mp4", VideoCodec: "h264", Width: 1920, Height: 1080, FPS: 24, CFR: true, DurationSec: 5}
	normalized := MiniMaxH3MediaProbe{Format: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPS: 30, CFR: true, DurationSec: 5}
	return original, normalized, nil
}

func TestSeedanceProviderAdapterRequiresPersistedAuthorizationBeforeClientCall(t *testing.T) {
	client := &stubSeedanceVideoClient{}
	adapter, err := NewSeedance20ProviderAdapter(Seedance20ProviderAdapterOptions{Enabled: true, Client: client, Downloader: stubSeedanceDownloader{}, Normalizer: stubSeedanceNormalizer{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Execute(context.Background(), GeneratedShotProviderExecutionRequest{Intent: validGeneratedShotIntent(), OutputDir: t.TempDir()})
	if err == nil || client.createCalls != 0 {
		t.Fatalf("Seedance client was reached without authorization: calls=%d err=%v", client.createCalls, err)
	}
}

func TestSeedanceProviderAdapterCreatesPollsNormalizesAndReviewsCandidate(t *testing.T) {
	client := &stubSeedanceVideoClient{}
	adapter, err := NewSeedance20ProviderAdapter(Seedance20ProviderAdapterOptions{
		Enabled: true, Client: client, Downloader: stubSeedanceDownloader{}, Normalizer: stubSeedanceNormalizer{}, PollInterval: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	outputDir := t.TempDir()
	result, err := adapter.Execute(context.Background(), GeneratedShotProviderExecutionRequest{
		Intent: validGeneratedShotIntent(), GenerationAuthorized: true, AuthorizationRef: "approval://seedance/1",
		IdempotencyKey: "seedance-idempotency", AdmissionScope: "final-film:seedance", OutputDir: outputDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.createCalls != 1 || client.pollCalls != 1 || result.ProviderTaskID != "seedance_task_1" || result.Candidate == nil || result.StructuralReview == nil {
		t.Fatalf("Seedance execution chain is incomplete: client=%+v result=%+v", client, result)
	}
	if result.Candidate.Provider != GeneratedShotProviderSeedance20 || result.Candidate.NormalizedArtifact.NormalizationProfile != GeneratedShotNormalizationProfile || !result.StructuralReview.StructurallyEligible {
		t.Fatalf("Seedance candidate did not enter the common review contract: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(outputDir, GeneratedShotProviderSeedance20, "seedance_task_1", "normalized.mp4")); err != nil {
		t.Fatal(err)
	}
}

func TestSeedanceProviderAdapterRejectsMissingOutput(t *testing.T) {
	adapter, _ := NewSeedance20ProviderAdapter(Seedance20ProviderAdapterOptions{Enabled: true, Client: failingSeedanceClient{}, Downloader: stubSeedanceDownloader{}, Normalizer: stubSeedanceNormalizer{}, PollAttempts: 1, PollInterval: 1})
	_, err := adapter.Execute(context.Background(), GeneratedShotProviderExecutionRequest{Intent: validGeneratedShotIntent(), GenerationAuthorized: true, AuthorizationRef: "approval://1", IdempotencyKey: "key", AdmissionScope: "scope", OutputDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected provider poll failure")
	}
}

func TestSeedanceOutputURLExtractionDoesNotTreatLastFrameAsVideo(t *testing.T) {
	output := map[string]any{
		"last_frame_url": "https://cdn.example.test/frame.png",
		"result":         map[string]any{"video_url": "https://cdn.example.test/signed-output"},
	}
	if got := firstHTTPSVideoURL(output); got != "https://cdn.example.test/signed-output" {
		t.Fatalf("unexpected Seedance video URL: %q", got)
	}
	if got := firstHTTPSVideoURL(map[string]any{"last_frame_url": "https://cdn.example.test/frame.png"}); got != "" {
		t.Fatalf("last-frame image was accepted as video: %q", got)
	}
}

type failingSeedanceClient struct{}

func (failingSeedanceClient) CreateContentGenerationTask(context.Context, ContentGenerationTaskRequest) (ContentGenerationTaskResult, error) {
	return ContentGenerationTaskResult{Response: &ContentGenerationTaskResponse{ID: "task", Status: "queued"}}, nil
}
func (failingSeedanceClient) GetContentGenerationTask(context.Context, string) (ContentGenerationTaskResult, error) {
	return ContentGenerationTaskResult{}, errors.New("poll failed")
}

type seedancePendingThenSucceededClient struct {
	createCalls int
	succeed     bool
}

func (c *seedancePendingThenSucceededClient) CreateContentGenerationTask(context.Context, ContentGenerationTaskRequest) (ContentGenerationTaskResult, error) {
	c.createCalls++
	return ContentGenerationTaskResult{Response: &ContentGenerationTaskResponse{ID: "seedance_task_pending", Status: "queued"}}, nil
}

func (c *seedancePendingThenSucceededClient) GetContentGenerationTask(context.Context, string) (ContentGenerationTaskResult, error) {
	if !c.succeed {
		return ContentGenerationTaskResult{Response: &ContentGenerationTaskResponse{ID: "seedance_task_pending", Status: "queued"}}, nil
	}
	return ContentGenerationTaskResult{Response: &ContentGenerationTaskResponse{ID: "seedance_task_pending", Status: "succeeded", Output: map[string]any{"content": map[string]any{"video_url": "https://cdn.example.test/seedance.mp4"}}}}, nil
}
