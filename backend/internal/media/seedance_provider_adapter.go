package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Seedance20ProviderAdapterOptions struct {
	Enabled          bool
	DisabledReason   string
	Client           VideoGenerationClient
	Downloader       MiniMaxH3OutputDownloader
	Normalizer       MiniMaxH3MediaNormalizer
	PollAttempts     int
	PollInterval     time.Duration
	MaxDownloadBytes int64
	Now              func() time.Time
}

type Seedance20ProviderAdapter struct {
	options Seedance20ProviderAdapterOptions
}

func NewSeedance20ProviderAdapter(options Seedance20ProviderAdapterOptions) (*Seedance20ProviderAdapter, error) {
	if options.Enabled && (options.Client == nil || options.Downloader == nil || options.Normalizer == nil) {
		return nil, errors.New("enabled Seedance 2.0 provider adapter requires client, downloader, and normalizer")
	}
	if options.PollAttempts <= 0 {
		options.PollAttempts = 180
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 5 * time.Second
	}
	if options.MaxDownloadBytes <= 0 {
		options.MaxDownloadBytes = defaultMiniMaxH3DownloadMaxBytes
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Seedance20ProviderAdapter{options: options}, nil
}

func (a *Seedance20ProviderAdapter) Descriptor() GeneratedShotProviderDescriptor {
	reason := strings.TrimSpace(a.options.DisabledReason)
	if !a.options.Enabled && reason == "" {
		reason = "Seedance 2.0 provider adapter is disabled by Server policy"
	}
	return GeneratedShotProviderDescriptor{
		Provider: GeneratedShotProviderSeedance20, Model: Seedance20ServerModel,
		ProfileVersion: (Seedance20GeneratedShotCompiler{}).Profile().ProfileVersion,
		Enabled:        a.options.Enabled, Reason: reason,
	}
}

func (a *Seedance20ProviderAdapter) Profile() GeneratedShotCapabilityProfile {
	return (Seedance20GeneratedShotCompiler{}).Profile()
}

func (a *Seedance20ProviderAdapter) Preflight(_ context.Context, intent GeneratedShotIntent) GeneratedShotProviderPreflight {
	report := PreflightGeneratedShotIntent(intent, []GeneratedShotProviderCompiler{Seedance20GeneratedShotCompiler{}}, []GeneratedShotProviderAvailability{{
		Provider: GeneratedShotProviderSeedance20, Enabled: a.options.Enabled, Reason: a.Descriptor().Reason,
	}})
	if len(report.Providers) == 0 {
		return GeneratedShotProviderPreflight{Provider: GeneratedShotProviderSeedance20, FailureCode: "provider_preflight_missing", FailureMessage: "Seedance 2.0 preflight produced no provider result"}
	}
	return report.Providers[0]
}

func (a *Seedance20ProviderAdapter) Execute(ctx context.Context, request GeneratedShotProviderExecutionRequest) (GeneratedShotProviderExecutionResult, error) {
	result := GeneratedShotProviderExecutionResult{
		SchemaVersion: GeneratedShotProviderExecutionSchemaVersion, Provider: GeneratedShotProviderSeedance20,
		Model: Seedance20ServerModel, IntentID: request.Intent.IntentID, Status: GeneratedShotFailureContinue,
		FailurePolicy: GeneratedShotFailureContinue,
	}
	fail := func(class string, err error) (GeneratedShotProviderExecutionResult, error) {
		result.ErrorClass = class
		if err != nil {
			result.ErrorMessage = err.Error()
		}
		return result, err
	}
	if !request.GenerationAuthorized || strings.TrimSpace(request.AuthorizationRef) == "" {
		return fail("generation_authorization_required", errors.New("explicit persisted generation authorization is required before provider execution"))
	}
	if !a.options.Enabled {
		return fail("provider_disabled", errors.New(a.Descriptor().Reason))
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.AdmissionScope) == "" || strings.TrimSpace(request.OutputDir) == "" {
		return fail("provider_execution_request_invalid", errors.New("idempotency_key, admission_scope, and output_dir are required"))
	}
	preflight := a.Preflight(ctx, request.Intent)
	if !preflight.Selectable {
		return fail(firstNonEmpty(preflight.FailureCode, "provider_preflight_failed"), errors.New(preflight.FailureMessage))
	}
	compiled, err := (Seedance20GeneratedShotCompiler{}).Compile(request.Intent)
	if err != nil {
		return fail("provider_request_compile_failed", err)
	}
	if request.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	created, err := a.options.Client.CreateContentGenerationTask(ctx, compiled.Request)
	if err != nil {
		return fail(firstNonEmpty(created.Trace.ErrorClass, "provider_create_failed"), err)
	}
	if created.Response == nil || strings.TrimSpace(created.Response.ID) == "" {
		return fail("provider_task_id_missing", errors.New("Seedance 2.0 create response is missing task ID"))
	}
	result.ProviderTaskID = strings.TrimSpace(created.Response.ID)
	videoURL, providerStatus, err := a.waitForOutput(ctx, result.ProviderTaskID, created)
	if err != nil {
		return fail("provider_task_"+firstNonEmpty(providerStatus, "failed"), err)
	}
	candidate, review, err := a.persistCandidate(ctx, request, result.ProviderTaskID, videoURL)
	if err != nil {
		return fail("provider_output_processing_failed", err)
	}
	result.Candidate = &candidate
	result.StructuralReview = &review
	result.Status = GeneratedShotCandidateReadyForReview
	return result, nil
}

func (a *Seedance20ProviderAdapter) waitForOutput(ctx context.Context, taskID string, initial ContentGenerationTaskResult) (string, string, error) {
	current := initial
	for attempt := 0; attempt <= a.options.PollAttempts; attempt++ {
		if current.Response != nil {
			status := normalizeSeedanceTaskStatus(current.Response.Status)
			if videoURL := firstHTTPSVideoURL(current.Response.Output); videoURL != "" {
				return videoURL, status, nil
			}
			switch status {
			case "failed", "cancelled", "expired":
				return "", status, fmt.Errorf("Seedance 2.0 task ended with status %s", status)
			}
		}
		if attempt == a.options.PollAttempts {
			break
		}
		if attempt > 0 || initial.Response != nil {
			timer := time.NewTimer(a.options.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", "cancelled", ctx.Err()
			case <-timer.C:
			}
		}
		queried, err := a.options.Client.GetContentGenerationTask(ctx, taskID)
		if err != nil {
			return "", firstNonEmpty(queried.Trace.ErrorClass, "poll_failed"), err
		}
		current = queried
	}
	return "", "pending", errors.New("Seedance 2.0 poll limit reached before output became available")
}

func (a *Seedance20ProviderAdapter) persistCandidate(ctx context.Context, request GeneratedShotProviderExecutionRequest, taskID, videoURL string) (GeneratedShotCandidate, GeneratedShotStructuralReview, error) {
	taskDir := filepath.Join(request.OutputDir, GeneratedShotProviderSeedance20, safeMiniMaxH3PathPart(taskID))
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return GeneratedShotCandidate{}, GeneratedShotStructuralReview{}, err
	}
	originalPath := filepath.Join(taskDir, "original.mp4")
	downloaded, err := a.options.Downloader.Download(ctx, videoURL, originalPath, a.options.MaxDownloadBytes)
	if err != nil {
		return GeneratedShotCandidate{}, GeneratedShotStructuralReview{}, err
	}
	normalizedPath := filepath.Join(taskDir, "normalized.mp4")
	originalProbe, normalizedProbe, err := a.options.Normalizer.Normalize(ctx, downloaded.Path, normalizedPath)
	if err != nil {
		return GeneratedShotCandidate{}, GeneratedShotStructuralReview{}, err
	}
	normalizedHash, normalizedSize, err := miniMaxH3FileDigest(normalizedPath)
	if err != nil {
		return GeneratedShotCandidate{}, GeneratedShotStructuralReview{}, err
	}
	if strings.EqualFold(normalizedHash, downloaded.SHA256) {
		return GeneratedShotCandidate{}, GeneratedShotStructuralReview{}, errors.New("normalized Seedance artifact must be distinct from provider original")
	}
	candidateID := "candidate_seedance_" + shortStableID(request.Intent.IntentID+":"+taskID)
	candidate := GeneratedShotCandidate{
		SchemaVersion: GeneratedShotCandidateSchemaVersion, CandidateID: candidateID, IntentID: request.Intent.IntentID,
		Provider: GeneratedShotProviderSeedance20, ProviderTaskID: taskID, Status: GeneratedShotCandidateReadyForReview,
		FailurePolicy: GeneratedShotFailureContinue, NonAuthoritative: true, PresentationOnly: true, RequiresExplicitReview: true,
		OriginalArtifact:   GeneratedShotCandidateArtifact{Role: "original", Path: downloaded.Path, MimeType: downloaded.MimeType, SHA256: downloaded.SHA256, SizeBytes: downloaded.SizeBytes, Probe: seedanceProbe(originalProbe)},
		NormalizedArtifact: GeneratedShotCandidateArtifact{Role: "normalized", Path: normalizedPath, MimeType: "video/mp4", SHA256: normalizedHash, SizeBytes: normalizedSize, Probe: seedanceProbe(normalizedProbe), NormalizationStatus: "ok", NormalizationProfile: GeneratedShotNormalizationProfile},
	}
	if err := ValidateGeneratedShotCandidate(candidate); err != nil {
		return GeneratedShotCandidate{}, GeneratedShotStructuralReview{}, err
	}
	review := ReviewGeneratedShotCandidateStructure("structural_"+shortStableID(candidateID), request.Intent, candidate)
	if !review.StructurallyEligible {
		return GeneratedShotCandidate{}, GeneratedShotStructuralReview{}, errors.New("normalized Seedance candidate failed structural review")
	}
	return candidate, review, nil
}

func (a *Seedance20ProviderAdapter) Cancel(context.Context, string) error {
	return errors.New("Seedance 2.0 cancellation is unavailable in the current Ark client")
}

func seedanceProbe(probe MiniMaxH3MediaProbe) GeneratedShotMediaProbe {
	return GeneratedShotMediaProbe{Format: probe.Format, VideoCodec: probe.VideoCodec, PixelFormat: probe.PixelFormat, Width: probe.Width, Height: probe.Height, FPS: probe.FPS, CFR: probe.CFR, DurationSec: probe.DurationSec}
}

func normalizeSeedanceTaskStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "in_queue", "pending":
		return "queued"
	case "running", "processing":
		return "running"
	case "succeeded", "success", "done", "completed":
		return "succeeded"
	case "failed", "error":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	case "expired":
		return "expired"
	default:
		return ""
	}
}

func firstHTTPSVideoURL(value any) string {
	return firstHTTPSVideoURLAt("", value)
}

func firstHTTPSVideoURLAt(path string, value any) string {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		lowerPath := strings.ToLower(path)
		lowerURL := strings.ToLower(trimmed)
		if strings.HasPrefix(lowerURL, "https://") && (strings.Contains(lowerPath, "video") || strings.Contains(lowerURL, ".mp4")) {
			return trimmed
		}
	case []any:
		for _, item := range typed {
			if found := firstHTTPSVideoURLAt(path, item); found != "" {
				return found
			}
		}
	case map[string]any:
		for key, item := range typed {
			nextPath := key
			if path != "" {
				nextPath = path + "." + key
			}
			if found := firstHTTPSVideoURLAt(nextPath, item); found != "" {
				return found
			}
		}
	}
	return ""
}

func shortStableID(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:8])
}
