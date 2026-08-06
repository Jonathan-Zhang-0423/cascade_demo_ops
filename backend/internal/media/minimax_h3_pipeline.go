package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultMiniMaxH3PollAttempts     = 1
	defaultMiniMaxH3DownloadMaxBytes = 512 << 20
	miniMaxH3ArtifactDirectory       = "minimax-h3"
)

type MiniMaxH3TaskQueryClient interface {
	GetContentGenerationTask(ctx context.Context, taskID string) (ContentGenerationTaskResult, error)
}

type MiniMaxH3OutputDownloader interface {
	Download(ctx context.Context, sourceURL string, destinationPath string, maxBytes int64) (MiniMaxH3DownloadedFile, error)
}

type MiniMaxH3MediaNormalizer interface {
	Normalize(ctx context.Context, sourcePath string, destinationPath string) (MiniMaxH3MediaProbe, MiniMaxH3MediaProbe, error)
}

type MiniMaxH3DownloadedFile struct {
	Path      string `json:"path"`
	MimeType  string `json:"mime_type"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type MiniMaxH3MediaProbe struct {
	Format      string  `json:"format,omitempty"`
	VideoCodec  string  `json:"video_codec,omitempty"`
	PixelFormat string  `json:"pixel_format,omitempty"`
	Width       int     `json:"width,omitempty"`
	Height      int     `json:"height,omitempty"`
	FPS         float64 `json:"fps,omitempty"`
	CFR         bool    `json:"cfr,omitempty"`
	DurationSec float64 `json:"duration_sec,omitempty"`
}

type MiniMaxH3Artifact struct {
	Role          string               `json:"role"`
	Path          string               `json:"path"`
	MimeType      string               `json:"mime_type"`
	SHA256        string               `json:"sha256"`
	SizeBytes     int64                `json:"size_bytes"`
	Probe         *MiniMaxH3MediaProbe `json:"probe,omitempty"`
	SourceTaskID  string               `json:"source_task_id"`
	SourceURL     string               `json:"source_url,omitempty"`
	Presentation  bool                 `json:"presentation_only"`
	Authoritative bool                 `json:"authoritative"`
}

type MiniMaxH3PollAttempt struct {
	Attempt    int               `json:"attempt"`
	Status     string            `json:"status,omitempty"`
	CheckedAt  time.Time         `json:"checked_at"`
	ErrorClass string            `json:"error_class,omitempty"`
	Trace      ArkMediaCallTrace `json:"trace"`
}

type MiniMaxH3PipelineResult struct {
	TaskID           string                 `json:"task_id"`
	Status           string                 `json:"status"`
	ProviderStatus   string                 `json:"provider_status,omitempty"`
	PollAttempts     []MiniMaxH3PollAttempt `json:"poll_attempts,omitempty"`
	OriginalArtifact *MiniMaxH3Artifact     `json:"original_artifact,omitempty"`
	Normalized       *MiniMaxH3Artifact     `json:"normalized_artifact,omitempty"`
	FailurePolicy    string                 `json:"failure_policy"`
	ErrorClass       string                 `json:"error_class,omitempty"`
	ErrorMessage     string                 `json:"error_message,omitempty"`
	Warnings         []string               `json:"warnings,omitempty"`
}

type MiniMaxH3PipelineOptions struct {
	Client           MiniMaxH3TaskQueryClient
	Downloader       MiniMaxH3OutputDownloader
	Normalizer       MiniMaxH3MediaNormalizer
	OutputDir        string
	PollAttempts     int
	PollInterval     time.Duration
	Timeout          time.Duration
	MaxDownloadBytes int64
	Now              func() time.Time
}

// CompleteMiniMaxH3Task runs the provider-output side of the isolated H3
// sidecar. It is deliberately not registered in the Server execution route.
// Any failure returns continue_without_generated_candidate so Browser Agent
// recordings and deterministic rendering remain independent.
func CompleteMiniMaxH3Task(ctx context.Context, taskID string, options MiniMaxH3PipelineOptions) MiniMaxH3PipelineResult {
	result := MiniMaxH3PipelineResult{
		TaskID:        strings.TrimSpace(taskID),
		Status:        "not_started",
		FailurePolicy: "continue_without_generated_candidate",
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.PollAttempts <= 0 {
		options.PollAttempts = defaultMiniMaxH3PollAttempts
	}
	if options.MaxDownloadBytes <= 0 {
		options.MaxDownloadBytes = defaultMiniMaxH3DownloadMaxBytes
	}
	if result.TaskID == "" {
		return failMiniMaxH3Pipeline(result, "task_id_missing", "task_id is required")
	}
	if options.Client == nil {
		return failMiniMaxH3Pipeline(result, "client_missing", "MiniMax-H3 query client is required")
	}
	if strings.TrimSpace(options.OutputDir) == "" {
		return failMiniMaxH3Pipeline(result, "output_dir_missing", "MiniMax-H3 output directory is required")
	}

	var videoURL string
	for attempt := 1; attempt <= options.PollAttempts; attempt++ {
		if attempt > 1 && options.PollInterval > 0 {
			timer := time.NewTimer(options.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return failMiniMaxH3Pipeline(result, "context_canceled", ctx.Err().Error())
			case <-timer.C:
			}
		}
		query, err := options.Client.GetContentGenerationTask(ctx, result.TaskID)
		poll := MiniMaxH3PollAttempt{Attempt: attempt, CheckedAt: options.Now().UTC(), Trace: query.Trace}
		if query.Response != nil {
			poll.Status = normalizeMiniMaxH3TaskStatus(query.Response.Status)
			result.ProviderStatus = poll.Status
			if value, ok := query.Response.Output["video_url"].(string); ok {
				videoURL = strings.TrimSpace(value)
			}
		}
		if err != nil {
			poll.ErrorClass = firstNonEmpty(query.Trace.ErrorClass, "provider_poll_failed")
			result.PollAttempts = append(result.PollAttempts, poll)
			if attempt < options.PollAttempts && miniMaxH3ErrorIsRetryable(poll.ErrorClass) {
				continue
			}
			return failMiniMaxH3Pipeline(result, poll.ErrorClass, err.Error())
		}
		result.PollAttempts = append(result.PollAttempts, poll)
		switch poll.Status {
		case MiniMaxH3TaskSucceeded:
			if videoURL == "" {
				return failMiniMaxH3Pipeline(result, "provider_output_missing", "succeeded MiniMax-H3 task is missing video_url")
			}
			attempt = options.PollAttempts
		case MiniMaxH3TaskFailed, MiniMaxH3TaskCancelled, MiniMaxH3TaskExpired:
			return failMiniMaxH3Pipeline(result, "provider_task_"+poll.Status, "MiniMax-H3 task ended with status "+poll.Status)
		case MiniMaxH3TaskQueued, MiniMaxH3TaskRunning:
			continue
		default:
			return failMiniMaxH3Pipeline(result, "provider_status_unknown", "MiniMax-H3 task returned an unsupported status")
		}
		break
	}
	if videoURL == "" {
		result.Status = "provider_task_pending"
		result.Warnings = append(result.Warnings, "poll limit reached before a generated candidate was available")
		return result
	}

	downloader := options.Downloader
	if downloader == nil {
		downloader = HTTPMiniMaxH3OutputDownloader{Client: http.DefaultClient}
	}
	taskDir := filepath.Join(options.OutputDir, miniMaxH3ArtifactDirectory, safeMiniMaxH3PathPart(result.TaskID))
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return failMiniMaxH3Pipeline(result, "artifact_dir_failed", err.Error())
	}
	originalPath := filepath.Join(taskDir, "original.mp4")
	downloaded, err := downloader.Download(ctx, videoURL, originalPath, options.MaxDownloadBytes)
	if err != nil {
		return failMiniMaxH3Pipeline(result, "provider_output_download_failed", err.Error())
	}
	result.OriginalArtifact = &MiniMaxH3Artifact{
		Role: "original", Path: downloaded.Path, MimeType: downloaded.MimeType,
		SHA256: downloaded.SHA256, SizeBytes: downloaded.SizeBytes,
		SourceTaskID: result.TaskID, SourceURL: videoURL, Presentation: true, Authoritative: false,
	}
	if options.Normalizer == nil {
		return failMiniMaxH3Pipeline(result, "media_normalizer_missing", "MiniMax-H3 media normalizer is required before editor use")
	}
	normalizedPath := filepath.Join(taskDir, "normalized.mp4")
	originalProbe, normalizedProbe, err := options.Normalizer.Normalize(ctx, downloaded.Path, normalizedPath)
	result.OriginalArtifact.Probe = &originalProbe
	if err != nil {
		return failMiniMaxH3Pipeline(result, "media_normalization_failed", err.Error())
	}
	normalizedHash, normalizedSize, err := miniMaxH3FileDigest(normalizedPath)
	if err != nil {
		return failMiniMaxH3Pipeline(result, "normalized_artifact_digest_failed", err.Error())
	}
	if normalizedHash == downloaded.SHA256 {
		return failMiniMaxH3Pipeline(result, "normalized_artifact_not_distinct", "normalized artifact must have a distinct SHA-256")
	}
	result.Normalized = &MiniMaxH3Artifact{
		Role: "normalized", Path: normalizedPath, MimeType: "video/mp4", SHA256: normalizedHash,
		SizeBytes: normalizedSize, Probe: &normalizedProbe, SourceTaskID: result.TaskID,
		Presentation: true, Authoritative: false,
	}
	result.Status = "normalized_candidate_ready_for_review"
	return result
}

type HTTPMiniMaxH3OutputDownloader struct {
	Client *http.Client
}

func (d HTTPMiniMaxH3OutputDownloader) Download(ctx context.Context, sourceURL string, destinationPath string, maxBytes int64) (MiniMaxH3DownloadedFile, error) {
	if strings.TrimSpace(sourceURL) == "" || strings.TrimSpace(destinationPath) == "" {
		return MiniMaxH3DownloadedFile{}, errors.New("source URL and destination path are required")
	}
	if maxBytes <= 0 {
		maxBytes = defaultMiniMaxH3DownloadMaxBytes
	}
	parsedURL, err := url.Parse(sourceURL)
	if err != nil || !strings.EqualFold(parsedURL.Scheme, "https") || strings.TrimSpace(parsedURL.Host) == "" {
		return MiniMaxH3DownloadedFile{}, errors.New("MiniMax-H3 output URL must be an absolute HTTPS URL")
	}
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return MiniMaxH3DownloadedFile{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return MiniMaxH3DownloadedFile{}, err
	}
	defer resp.Body.Close()
	if resp.Request == nil || resp.Request.URL == nil || !strings.EqualFold(resp.Request.URL.Scheme, "https") {
		return MiniMaxH3DownloadedFile{}, errors.New("MiniMax-H3 output download redirect must remain on HTTPS")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return MiniMaxH3DownloadedFile{}, fmt.Errorf("MiniMax-H3 output download HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return MiniMaxH3DownloadedFile{}, fmt.Errorf("MiniMax-H3 output exceeds max download bytes: %d > %d", resp.ContentLength, maxBytes)
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return MiniMaxH3DownloadedFile{}, err
	}
	temporaryPath := destinationPath + ".tmp"
	out, err := os.Create(temporaryPath)
	if err != nil {
		return MiniMaxH3DownloadedFile{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(out, hash), &io.LimitedReader{R: resp.Body, N: maxBytes + 1})
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || size > maxBytes {
		_ = os.Remove(temporaryPath)
		if copyErr != nil {
			return MiniMaxH3DownloadedFile{}, copyErr
		}
		if closeErr != nil {
			return MiniMaxH3DownloadedFile{}, closeErr
		}
		return MiniMaxH3DownloadedFile{}, fmt.Errorf("MiniMax-H3 output exceeds max download bytes: %d > %d", size, maxBytes)
	}
	if err := os.Rename(temporaryPath, destinationPath); err != nil {
		_ = os.Remove(temporaryPath)
		return MiniMaxH3DownloadedFile{}, err
	}
	mimeType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	return MiniMaxH3DownloadedFile{Path: destinationPath, MimeType: mimeType, SHA256: hex.EncodeToString(hash.Sum(nil)), SizeBytes: size}, nil
}

func miniMaxH3ErrorIsRetryable(class string) bool {
	switch strings.TrimSpace(class) {
	case "http_error", "provider_rate_limited", "provider_server_error", "provider_overloaded":
		return true
	default:
		return false
	}
}

func failMiniMaxH3Pipeline(result MiniMaxH3PipelineResult, class string, message string) MiniMaxH3PipelineResult {
	result.Status = "continue_without_generated_candidate"
	result.ErrorClass = strings.TrimSpace(class)
	result.ErrorMessage = strings.TrimSpace(message)
	return result
}

func miniMaxH3FileDigest(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func safeMiniMaxH3PathPart(value string) string {
	value = strings.TrimSpace(value)
	var out strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out.WriteRune(r)
		} else {
			out.WriteByte('_')
		}
	}
	if out.Len() == 0 {
		return "task"
	}
	return out.String()
}
