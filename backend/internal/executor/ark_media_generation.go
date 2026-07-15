package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

const (
	defaultArkMediaPollAttempts     = 1
	defaultArkMediaPollInterval     = 0
	defaultArkMediaDownloadMaxBytes = 512 << 20
	arkMediaOutputDownloadDirectory = "ark-media"
)

type ArkMediaGenerationOptions struct {
	OutputDir        string
	Client           media.ArkMediaClient
	PollAttempts     int
	PollInterval     time.Duration
	Downloader       arkMediaCandidateDownloader
	MaxDownloadBytes int64
	Now              func() time.Time
}

type arkMediaCandidateDownloader interface {
	Download(ctx context.Context, sourceURL string, destPath string, maxBytes int64) (downloadedArkMediaCandidate, error)
}

type downloadedArkMediaCandidate struct {
	Path      string
	MimeType  string
	SHA256    string
	SizeBytes int64
}

type httpArkMediaCandidateDownloader struct {
	client *http.Client
}

func NewArkMediaGenerationResultWithOptions(ctx context.Context, source *model.ClientExecutionPackage, suggestion model.DirectorEditSuggestion, createdAt time.Time, options ArkMediaGenerationOptions) model.ArkMediaGenerationResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.MaxDownloadBytes <= 0 {
		options.MaxDownloadBytes = defaultArkMediaDownloadMaxBytes
	}
	result := NewArkMediaGenerationResult(source, suggestion, createdAt)
	if result.ProviderCall != nil {
		pollArkMediaProviderTask(ctx, &result, sourcePackageIDForGeneration(source, suggestion), createdAt, options)
	}
	if len(result.CandidateArtifacts) > 0 && strings.TrimSpace(options.OutputDir) != "" {
		downloader := options.Downloader
		if downloader == nil {
			downloader = httpArkMediaCandidateDownloader{client: http.DefaultClient}
		}
		downloaded, warnings := downloadArkMediaCandidateArtifacts(ctx, sourcePackageIDForGeneration(source, suggestion), result.CandidateArtifacts, filepath.Join(options.OutputDir, arkMediaOutputDownloadDirectory), downloader, options.MaxDownloadBytes, createdAt)
		result.DownloadedArtifacts = downloaded
		result.Warnings = appendReadinessFindings(result.Warnings, warnings...)
		if len(downloaded) > 0 {
			result.Status = "candidate_artifacts_downloaded"
			result.Warnings = removeReadinessFindingsByCode(result.Warnings, "no_candidate_urls")
		}
	}
	return result
}

func arkMediaGenerationOptionsFromEnv(outputDir string, now func() time.Time) ArkMediaGenerationOptions {
	mode := configuredArkMediaModeFromEnv()
	pollAttempts := 0
	client := media.ArkMediaClient(nil)
	if mode == config.ArkMediaModeReal {
		pollAttempts = defaultArkMediaPollAttempts
		client = arkMediaClientFromEnv(mode)
	}
	pollAttempts = intFromEnv("CASCADE_ARK_MEDIA_POLL_ATTEMPTS", pollAttempts)
	if pollAttempts < 0 {
		pollAttempts = 0
	}
	return ArkMediaGenerationOptions{
		OutputDir:        envOrDefaultTrimmed("CASCADE_ARK_MEDIA_OUTPUT_DIR", outputDir),
		Client:           client,
		PollAttempts:     pollAttempts,
		PollInterval:     durationFromMillisEnv("CASCADE_ARK_MEDIA_POLL_INTERVAL_MS", defaultArkMediaPollInterval),
		MaxDownloadBytes: int64FromEnv("CASCADE_ARK_MEDIA_DOWNLOAD_MAX_BYTES", defaultArkMediaDownloadMaxBytes),
		Now:              now,
	}
}

func pollArkMediaProviderTask(ctx context.Context, result *model.ArkMediaGenerationResult, sourcePackageID string, createdAt time.Time, options ArkMediaGenerationOptions) {
	if result == nil || result.ProviderCall == nil || options.Client == nil || options.PollAttempts <= 0 || strings.TrimSpace(result.TaskID) == "" {
		return
	}
	if len(result.CandidateArtifacts) > 0 || result.Status == "provider_call_failed" {
		return
	}
	for attempt := 1; attempt <= options.PollAttempts; attempt++ {
		if attempt > 1 && options.PollInterval > 0 {
			select {
			case <-ctx.Done():
				appendPollFailure(result, attempt, options.Now(), "context_canceled", ctx.Err().Error())
				return
			case <-time.After(options.PollInterval):
			}
		}
		pollResult, err := options.Client.GetContentGenerationTask(ctx, result.TaskID)
		call := providerCallFromPollResult(*result.ProviderCall, pollResult, err)
		candidates := providerCandidateArtifacts(sourcePackageID, &call, createdAt)
		result.ProviderCall = &call
		result.ProviderStatus = call.ProviderStatus
		result.CandidateArtifacts = candidates
		status := generationResultStatus(call, len(candidates))
		result.Status = status
		pollAttempt := model.ArkMediaProviderPollAttempt{
			Attempt:           attempt,
			CheckedAt:         options.Now().UTC(),
			Status:            status,
			ProviderStatus:    call.ProviderStatus,
			CandidateURLCount: len(candidates),
			Trace:             call.Trace,
			ErrorClass:        call.ErrorClass,
			ErrorMessage:      call.ErrorMessage,
		}
		result.PollAttempts = append(result.PollAttempts, pollAttempt)
		if err != nil {
			result.Warnings = appendReadinessFindings(result.Warnings, model.ArkMediaReadinessFinding{
				Code:    firstNonEmptyString(call.ErrorClass, "provider_poll_failed"),
				Message: firstNonEmptyString(call.ErrorMessage, "provider task polling failed"),
				TaskID:  result.TaskID,
			})
			return
		}
		if len(candidates) > 0 {
			result.Warnings = removeReadinessFindingsByCode(result.Warnings, "no_candidate_urls")
			return
		}
		if providerStatusIsTerminal(call.ProviderStatus) {
			return
		}
	}
}

func providerCallFromPollResult(base model.DirectorProviderCall, result media.ContentGenerationTaskResult, err error) model.DirectorProviderCall {
	call := base
	if result.Provider != "" {
		call.Provider = string(result.Provider)
	}
	call.Model = firstNonEmptyString(result.Model, call.Model)
	if result.Trace.Method != "" {
		call.Trace = directorProviderTrace(result.Trace)
	}
	if result.Response != nil {
		call.TaskID = firstNonEmptyString(result.Response.ID, call.TaskID)
		call.ProviderStatus = firstNonEmptyString(result.Response.Status, call.ProviderStatus)
		call.Model = firstNonEmptyString(result.Response.Model, call.Model)
		if len(result.Response.Output) > 0 {
			call.Output = result.Response.Output
			call.Status = "provider_output_polled"
		} else {
			call.Status = "provider_task_polled"
		}
		if result.Response.Error != nil {
			call.ErrorClass = firstNonEmptyString(result.Response.Error.Type, result.Response.Error.Code)
			call.ErrorMessage = result.Response.Error.Message
			call.Status = "provider_error"
		}
	}
	if err != nil {
		call.Status = "failed"
		call.ErrorClass = firstNonEmptyString(result.Trace.ErrorClass, "provider_poll_failed")
		call.ErrorMessage = err.Error()
	}
	return call
}

func downloadArkMediaCandidateArtifacts(ctx context.Context, sourcePackageID string, candidates []model.ArtifactRef, downloadDir string, downloader arkMediaCandidateDownloader, maxBytes int64, createdAt time.Time) ([]model.ArtifactRef, []model.ArkMediaReadinessFinding) {
	if len(candidates) == 0 || strings.TrimSpace(downloadDir) == "" || downloader == nil {
		return nil, nil
	}
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		return nil, []model.ArkMediaReadinessFinding{{
			Code:    "candidate_download_dir_failed",
			Message: err.Error(),
		}}
	}
	downloaded := []model.ArtifactRef{}
	warnings := []model.ArkMediaReadinessFinding{}
	for index, candidate := range candidates {
		if !isHTTPURL(candidate.URI) {
			continue
		}
		destPath := filepath.Join(downloadDir, arkMediaCandidateFileName(index+1, candidate))
		file, err := downloader.Download(ctx, candidate.URI, destPath, maxBytes)
		if err != nil {
			warnings = append(warnings, model.ArkMediaReadinessFinding{
				Code:    "candidate_download_failed",
				Message: err.Error(),
				RefID:   candidate.ID,
				TaskID:  artifactStringMetadata(candidate.Metadata, "task_id"),
			})
			continue
		}
		metadata := cloneArtifactMetadata(candidate.Metadata)
		metadata["provider_output_url"] = candidate.URI
		metadata["provider_output_artifact_id"] = candidate.ID
		metadata["download_status"] = "downloaded"
		metadata["include_in_demo"] = false
		metadata["source_material_policy"] = "non_authoritative_generated_candidate"
		downloaded = append(downloaded, model.ArtifactRef{
			ID:        artifactID(sourcePackageID, candidate.Kind+"_downloaded", index+1),
			Kind:      candidate.Kind,
			URI:       file.Path,
			MimeType:  firstNonEmptyString(file.MimeType, candidate.MimeType),
			SHA256:    file.SHA256,
			SizeBytes: file.SizeBytes,
			CreatedAt: createdAt,
			Sensitive: false,
			Metadata:  metadata,
		})
	}
	return downloaded, warnings
}

func (d httpArkMediaCandidateDownloader) Download(ctx context.Context, sourceURL string, destPath string, maxBytes int64) (downloadedArkMediaCandidate, error) {
	if strings.TrimSpace(sourceURL) == "" {
		return downloadedArkMediaCandidate{}, errors.New("source URL is required")
	}
	if strings.TrimSpace(destPath) == "" {
		return downloadedArkMediaCandidate{}, errors.New("destination path is required")
	}
	if maxBytes <= 0 {
		maxBytes = defaultArkMediaDownloadMaxBytes
	}
	client := d.client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return downloadedArkMediaCandidate{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return downloadedArkMediaCandidate{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return downloadedArkMediaCandidate{}, fmt.Errorf("download HTTP %d from provider output URL", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return downloadedArkMediaCandidate{}, fmt.Errorf("provider output exceeds max download bytes: %d > %d", resp.ContentLength, maxBytes)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return downloadedArkMediaCandidate{}, err
	}
	tmpPath := destPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return downloadedArkMediaCandidate{}, err
	}
	hash := sha256.New()
	limited := &io.LimitedReader{R: resp.Body, N: maxBytes + 1}
	size, copyErr := io.Copy(io.MultiWriter(out, hash), limited)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return downloadedArkMediaCandidate{}, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return downloadedArkMediaCandidate{}, closeErr
	}
	if size > maxBytes {
		_ = os.Remove(tmpPath)
		return downloadedArkMediaCandidate{}, fmt.Errorf("provider output exceeds max download bytes: %d > %d", size, maxBytes)
	}
	_ = os.Remove(destPath)
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return downloadedArkMediaCandidate{}, err
	}
	contentType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	return downloadedArkMediaCandidate{
		Path:      destPath,
		MimeType:  firstNonEmptyString(contentType, mimeTypeForPath(destPath, "")),
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
		SizeBytes: size,
	}, nil
}

func appendPollFailure(result *model.ArkMediaGenerationResult, attempt int, checkedAt time.Time, class string, message string) {
	result.PollAttempts = append(result.PollAttempts, model.ArkMediaProviderPollAttempt{
		Attempt:      attempt,
		CheckedAt:    checkedAt.UTC(),
		Status:       "provider_poll_failed",
		ErrorClass:   class,
		ErrorMessage: message,
	})
	result.Warnings = appendReadinessFindings(result.Warnings, model.ArkMediaReadinessFinding{
		Code:    class,
		Message: message,
		TaskID:  result.TaskID,
	})
}

func arkMediaCandidateFileName(index int, candidate model.ArtifactRef) string {
	role := artifactStringMetadata(candidate.Metadata, "asset_role")
	if role == "" {
		role = candidate.Kind
	}
	ext := arkMediaCandidateExtension(candidate)
	return fmt.Sprintf("%03d-%s%s", index, safeID(firstNonEmptyString(role, "candidate")), ext)
}

func arkMediaCandidateExtension(candidate model.ArtifactRef) string {
	ext := providerURLExtension(candidate.URI)
	switch ext {
	case ".mp4", ".mov", ".webm", ".png", ".jpg", ".jpeg", ".webp":
		return ext
	}
	switch candidate.MimeType {
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "video/mp4":
		return ".mp4"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/png":
		return ".png"
	default:
		return ".bin"
	}
}

func providerStatusIsTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "success", "completed", "complete", "done", "failed", "error", "cancelled", "canceled", "expired":
		return true
	default:
		return false
	}
}

func sourcePackageIDForGeneration(source *model.ClientExecutionPackage, suggestion model.DirectorEditSuggestion) string {
	if source != nil && source.PackageID != "" {
		return source.PackageID
	}
	return suggestion.SourcePackageID
}

func appendReadinessFindings(left []model.ArkMediaReadinessFinding, right ...model.ArkMediaReadinessFinding) []model.ArkMediaReadinessFinding {
	out := append([]model.ArkMediaReadinessFinding{}, left...)
	out = append(out, right...)
	return out
}

func removeReadinessFindingsByCode(values []model.ArkMediaReadinessFinding, code string) []model.ArkMediaReadinessFinding {
	out := []model.ArkMediaReadinessFinding{}
	for _, value := range values {
		if value.Code == code {
			continue
		}
		out = append(out, value)
	}
	return out
}

func cloneArtifactMetadata(metadata map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func artifactStringMetadata(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func intFromEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func int64FromEnv(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func durationFromMillisEnv(key string, fallback time.Duration) time.Duration {
	value := int64FromEnv(key, int64(fallback/time.Millisecond))
	if value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Millisecond
}
