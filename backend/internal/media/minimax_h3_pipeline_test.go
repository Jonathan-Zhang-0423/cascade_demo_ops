package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
)

type stubMiniMaxH3QueryClient struct {
	results []ContentGenerationTaskResult
	errors  []error
	calls   int
}

func (s *stubMiniMaxH3QueryClient) GetContentGenerationTask(context.Context, string) (ContentGenerationTaskResult, error) {
	index := s.calls
	s.calls++
	if index >= len(s.results) {
		return ContentGenerationTaskResult{}, errors.New("unexpected query")
	}
	var err error
	if index < len(s.errors) {
		err = s.errors[index]
	}
	return s.results[index], err
}

type stubMiniMaxH3Downloader struct {
	content []byte
	calls   int
}

func (s *stubMiniMaxH3Downloader) Download(_ context.Context, _ string, destinationPath string, _ int64) (MiniMaxH3DownloadedFile, error) {
	s.calls++
	if err := os.WriteFile(destinationPath, s.content, 0o644); err != nil {
		return MiniMaxH3DownloadedFile{}, err
	}
	hash := sha256.Sum256(s.content)
	return MiniMaxH3DownloadedFile{Path: destinationPath, MimeType: "video/mp4", SHA256: hex.EncodeToString(hash[:]), SizeBytes: int64(len(s.content))}, nil
}

type stubMiniMaxH3Normalizer struct {
	calls int
}

func (s *stubMiniMaxH3Normalizer) Normalize(_ context.Context, _ string, destinationPath string) (MiniMaxH3MediaProbe, MiniMaxH3MediaProbe, error) {
	s.calls++
	if err := os.WriteFile(destinationPath, []byte("normalized-video"), 0o644); err != nil {
		return MiniMaxH3MediaProbe{}, MiniMaxH3MediaProbe{}, err
	}
	return MiniMaxH3MediaProbe{Format: "mov,mp4", VideoCodec: "hevc", Width: 2048, Height: 1152, FPS: 24, DurationSec: 5},
		MiniMaxH3MediaProbe{Format: "mov,mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPS: 30, CFR: true, DurationSec: 5}, nil
}

func TestCompleteMiniMaxH3TaskPollsDownloadsAndNormalizes(t *testing.T) {
	client := &stubMiniMaxH3QueryClient{results: []ContentGenerationTaskResult{
		{Provider: config.ModelProvider("minimax-h3"), Response: &ContentGenerationTaskResponse{ID: "task_1", Status: MiniMaxH3TaskRunning}},
		{Provider: config.ModelProvider("minimax-h3"), Response: &ContentGenerationTaskResponse{ID: "task_1", Status: MiniMaxH3TaskSucceeded, Output: map[string]any{"video_url": "https://cdn.example.test/video.mp4"}}},
	}}
	downloader := &stubMiniMaxH3Downloader{content: []byte("original-video")}
	normalizer := &stubMiniMaxH3Normalizer{}
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	result := CompleteMiniMaxH3Task(t.Context(), "task_1", MiniMaxH3PipelineOptions{
		Client: client, Downloader: downloader, Normalizer: normalizer, OutputDir: t.TempDir(), PollAttempts: 2, Now: func() time.Time { return now },
	})
	if result.Status != "normalized_candidate_ready_for_review" || result.ErrorClass != "" {
		t.Fatalf("result = %+v", result)
	}
	if client.calls != 2 || downloader.calls != 1 || normalizer.calls != 1 {
		t.Fatalf("calls: query=%d download=%d normalize=%d", client.calls, downloader.calls, normalizer.calls)
	}
	if result.OriginalArtifact == nil || result.Normalized == nil || result.OriginalArtifact.SHA256 == result.Normalized.SHA256 {
		t.Fatalf("artifacts = original=%+v normalized=%+v", result.OriginalArtifact, result.Normalized)
	}
	if !result.OriginalArtifact.Presentation || result.OriginalArtifact.Authoritative || !result.Normalized.Presentation || result.Normalized.Authoritative {
		t.Fatalf("authority flags = original=%+v normalized=%+v", result.OriginalArtifact, result.Normalized)
	}
	if filepath.Base(result.OriginalArtifact.Path) != "original.mp4" || filepath.Base(result.Normalized.Path) != "normalized.mp4" {
		t.Fatalf("artifact paths = %q %q", result.OriginalArtifact.Path, result.Normalized.Path)
	}
}

func TestCompleteMiniMaxH3TaskFailureDoesNotProduceCandidate(t *testing.T) {
	client := &stubMiniMaxH3QueryClient{results: []ContentGenerationTaskResult{{Response: &ContentGenerationTaskResponse{ID: "task_1", Status: MiniMaxH3TaskFailed}}}}
	downloader := &stubMiniMaxH3Downloader{content: []byte("must-not-download")}
	result := CompleteMiniMaxH3Task(t.Context(), "task_1", MiniMaxH3PipelineOptions{Client: client, Downloader: downloader, OutputDir: t.TempDir()})
	if result.Status != "continue_without_generated_candidate" || result.ErrorClass != "provider_task_failed" {
		t.Fatalf("result = %+v", result)
	}
	if downloader.calls != 0 || result.OriginalArtifact != nil || result.Normalized != nil {
		t.Fatalf("unexpected candidate work: downloader=%d result=%+v", downloader.calls, result)
	}
}

func TestCompleteMiniMaxH3TaskNeverMarksOriginalEditorReady(t *testing.T) {
	client := &stubMiniMaxH3QueryClient{results: []ContentGenerationTaskResult{{Response: &ContentGenerationTaskResponse{ID: "task_1", Status: MiniMaxH3TaskSucceeded, Output: map[string]any{"video_url": "https://cdn.example.test/video.mp4"}}}}}
	downloader := &stubMiniMaxH3Downloader{content: []byte("original-video")}
	result := CompleteMiniMaxH3Task(t.Context(), "task_1", MiniMaxH3PipelineOptions{Client: client, Downloader: downloader, OutputDir: t.TempDir()})
	if result.Status != "continue_without_generated_candidate" || result.ErrorClass != "media_normalizer_missing" {
		t.Fatalf("result = %+v", result)
	}
	if result.OriginalArtifact == nil || result.Normalized != nil {
		t.Fatalf("artifacts = original=%+v normalized=%+v", result.OriginalArtifact, result.Normalized)
	}
}

func TestCompleteMiniMaxH3TaskRetriesOnlyRetryableProviderErrors(t *testing.T) {
	client := &stubMiniMaxH3QueryClient{
		results: []ContentGenerationTaskResult{
			{Trace: ArkMediaCallTrace{ErrorClass: "provider_rate_limited"}},
			{Response: &ContentGenerationTaskResponse{ID: "task_1", Status: MiniMaxH3TaskRunning}},
		},
		errors: []error{errors.New("rate limited"), nil},
	}
	result := CompleteMiniMaxH3Task(t.Context(), "task_1", MiniMaxH3PipelineOptions{Client: client, OutputDir: t.TempDir(), PollAttempts: 2})
	if client.calls != 2 || result.Status != "provider_task_pending" {
		t.Fatalf("calls=%d result=%+v", client.calls, result)
	}
}

func TestCompleteMiniMaxH3TaskHonorsTimeoutWithoutCandidate(t *testing.T) {
	client := &blockingMiniMaxH3QueryClient{}
	result := CompleteMiniMaxH3Task(t.Context(), "task_1", MiniMaxH3PipelineOptions{
		Client: client, OutputDir: t.TempDir(), PollAttempts: 1, Timeout: time.Millisecond,
	})
	if result.Status != "continue_without_generated_candidate" || result.ErrorClass != "context_canceled" {
		t.Fatalf("result = %+v", result)
	}
}

type blockingMiniMaxH3QueryClient struct{}

func (*blockingMiniMaxH3QueryClient) GetContentGenerationTask(ctx context.Context, _ string) (ContentGenerationTaskResult, error) {
	<-ctx.Done()
	return ContentGenerationTaskResult{Trace: ArkMediaCallTrace{ErrorClass: "context_canceled"}}, ctx.Err()
}
