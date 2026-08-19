// Command seedancepreflight performs one small, explicitly authorized real
// Seedance 2.5 task using a selected local reference asset. It never changes an
// edit plan and writes only redacted task/output metadata.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

func main() {
	source := flag.String("source", "", "local source_reference.mp4")
	taskIDFlag := flag.String("task-id", "", "resume polling an existing Seedance task without creating a new task")
	ffmpegFlag := flag.String("ffmpeg", "", "ffmpeg executable used to normalize downloaded candidates")
	ffprobeFlag := flag.String("ffprobe", "", "ffprobe executable used to validate a new local reference before upload")
	retentionAck := flag.Bool("tos-retention-ack", false, "confirm the client acknowledged the standard 30-day private-TOS retention before a new upload")
	outDir := flag.String("output", "artifacts/seedance-preflight/latest", "candidate output directory")
	pollAttempts := flag.Int("poll-attempts", 20, "maximum task polls")
	pollInterval := flag.Duration("poll-interval", 5*time.Second, "delay between polls")
	flag.Parse()
	taskID := strings.TrimSpace(*taskIDFlag)
	if strings.TrimSpace(*source) == "" && taskID == "" {
		fatal(errors.New("-source is required when -task-id is empty"))
	}
	if *pollAttempts < 1 || *pollAttempts > 120 {
		fatal(errors.New("-poll-attempts must be between 1 and 120"))
	}
	cwd, err := os.Getwd()
	fatalIf(err)
	repoRoot := config.DiscoverDevRepoRoot(cwd)
	fatalIf(config.LoadDotEnvFiles(config.DefaultDotEnvPaths(repoRoot)...))
	runtime, err := config.RuntimeConfigFromEnvWithRoot(repoRoot)
	fatalIf(err)
	if runtime.ArkMediaMode != config.ArkMediaModeReal {
		fatal(errors.New("CASCADE_ARK_MEDIA_MODE must be real"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	client := media.NewClient(runtime, nil)
	created := time.Now().UTC()
	retention := model.DefaultMediaDeliveryPreferences().TOSRetention
	retention.ClientDisclosureAcknowledged = *retentionAck
	audit := map[string]any{"schema_version": "cascade.seedance_preflight.v1", "created_at": created, "last_checked_at": created, "provider": config.ModelProviderSeedance, "model": runtime.ModelProviders[config.ModelProviderSeedance].DefaultModel, "real_call_made": false, "review_state": "candidate_only", "provider_output_adopted": false, "inserted_into_demo_edit_plan": false, "tos_retention": retention}
	if taskID != "" {
		previous, err := readExistingAudit(*outDir)
		fatalIf(err)
		if previous != nil {
			audit = previous
		}
		audit["task_id"] = taskID
		audit["task_resumed"] = true
		audit["last_checked_at"] = created
	}
	if taskID == "" {
		info, err := os.Stat(*source)
		fatalIf(err)
		if info.IsDir() {
			fatal(errors.New("source must be a file"))
		}
		probe, err := probeSeedanceReference(ctx, firstNonEmpty(*ffprobeFlag, runtime.FFprobePath, "ffprobe"), *source)
		audit["source_probe"] = probe
		if err != nil {
			audit["status"] = "source_preflight_failed"
			audit["error_class"] = "source_reference_invalid"
			writeJSON(*outDir, "seedance_audit.json", audit)
			fatal(err)
		}
		sourcePackageID := seedancePreflightSourcePackageID(created)
		audit["source_package_id"] = sourcePackageID
		plan := model.ArkAssetPublicationPlan{
			SchemaVersion: model.ArkAssetPublicationPlanSchemaVersion, PlanID: "seedance_preflight_publication",
			CreatedAt: time.Now().UTC(), Mode: "real_preflight", SourcePackageID: sourcePackageID, TOSRetention: retention,
			Items: []model.ArkAssetPublicationItem{{
				Ref:     model.DirectorMaterialRef{ID: "source_reference_video", Kind: "raw_recording", URI: *source, MimeType: "video/mp4"},
				TaskIDs: []string{"seedance_preflight"}, Usage: "presentation_reference", Required: true,
				RecommendedFileName: "source_reference.mp4", Status: "ready_after_publication",
			}},
		}
		tosConfig, configured := media.TOSAssetPublisherConfigFromEnv(os.Getenv)
		if !configured {
			fatal(errors.New("TOS configuration is missing"))
		}
		publisher, err := media.NewTOSAssetPublisher(tosConfig, time.Now)
		fatalIf(err)
		pub, err := publisher.PublishArkAssets(ctx, plan, model.DirectorMaterialRef{ID: "seedance_preflight_publication"})
		fatalIf(err)
		if !pub.CanUseForRealCall || len(pub.Items) == 0 || pub.Items[0].ProposedPublicRef == nil {
			writeJSON(*outDir, "publication_result.json", pub)
			if publicationRequiresRetentionAck(pub) {
				fatal(errors.New("TOS publication is blocked until the client acknowledges standard 30-day retention; rerun with -tos-retention-ack only after that acknowledgement"))
			}
			fatal(errors.New("TOS publication did not produce a provider-ready URL"))
		}
		ref := pub.Items[0].ProposedPublicRef
		request := seedancePreflightRequest(runtime.ModelProviders[config.ModelProviderSeedance].DefaultModel, ref.URI)
		audit["request_profile"] = seedancePreflightRequestProfile(request)
		initial, err := client.CreateContentGenerationTask(ctx, request)
		audit["provider"] = initial.Provider
		audit["model"] = initial.Model
		audit["real_call_made"] = initial.Trace.HTTPStatus != 0
		audit["request_trace"] = initial.Trace
		audit["task_id"] = responseID(initial.Response)
		audit["provider_status"] = responseStatus(initial.Response)
		audit["publication"] = map[string]any{"mode": pub.Mode, "can_use_for_real_call": pub.CanUseForRealCall, "item_status": pub.Items[0].Status}
		if err != nil {
			audit["error_class"] = initial.Trace.ErrorClass
			audit["status"] = "provider_call_failed"
			writeJSON(*outDir, "seedance_audit.json", audit)
			fatal(err)
		}
		taskID = responseID(initial.Response)
		if taskID == "" {
			writeJSON(*outDir, "seedance_audit.json", audit)
			fatal(errors.New("Seedance response missing task id"))
		}
	}
	lastProviderStatus := ""
	for attempt := 1; attempt <= *pollAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				fatal(ctx.Err())
			case <-time.After(*pollInterval):
			}
		}
		polled, pollErr := client.GetContentGenerationTask(ctx, taskID)
		audit["poll_attempt"] = attempt
		audit["poll_trace"] = polled.Trace
		audit["real_call_made"] = true
		lastProviderStatus = responseStatus(polled.Response)
		audit["provider_status"] = lastProviderStatus
		if pollErr != nil {
			audit["error_class"] = polled.Trace.ErrorClass
			writeJSON(*outDir, "seedance_audit.json", audit)
			fatal(pollErr)
		}
		urls := extractURLs(polled.Response)
		if len(urls) > 0 {
			candidateFiles := make([]string, 0, len(urls))
			for index, raw := range urls {
				path := download(ctx, raw, filepath.Join(*outDir, fmt.Sprintf("candidate-%02d", index+1)))
				candidateFiles = append(candidateFiles, filepath.Base(path))
			}
			normalizedFiles, normalizationErrors, normalizationProbes := normalizeCandidateFiles(ctx, candidateFiles, *outDir, *ffmpegFlag, *ffprobeFlag, runtime)
			audit["candidate_url_count"] = len(urls)
			audit["candidate_files"] = candidateFiles
			audit["normalized_candidate_files"] = normalizedFiles
			audit["normalization_probes"] = normalizationProbes
			artifactManifest := candidateArtifactManifest(candidateFiles, normalizedFiles, *outDir)
			audit["candidate_artifacts"] = artifactManifest
			audit["candidate_integrity_verified"] = candidateArtifactManifestVerified(artifactManifest)
			if len(normalizationErrors) > 0 {
				audit["normalization_errors"] = normalizationErrors
			} else {
				delete(audit, "normalization_errors")
			}
			if len(normalizedFiles) > 0 && candidateArtifactManifestVerified(artifactManifest) {
				audit["status"] = "candidate_downloaded_and_normalized"
			} else if len(normalizedFiles) > 0 {
				audit["status"] = "candidate_downloaded_integrity_failed"
			} else {
				audit["status"] = "candidate_downloaded_normalization_failed"
			}
			writeJSON(*outDir, "seedance_audit.json", audit)
			if len(normalizedFiles) > 0 && candidateArtifactManifestVerified(artifactManifest) {
				fmt.Printf("Seedance preflight passed; candidate_count=%d; normalized_count=%d; audit=%s\n", len(urls), len(normalizedFiles), filepath.Join(*outDir, "seedance_audit.json"))
			} else if len(normalizedFiles) > 0 {
				fmt.Printf("Seedance candidate downloaded but integrity verification failed; candidate_count=%d; audit=%s\n", len(urls), filepath.Join(*outDir, "seedance_audit.json"))
			} else {
				fmt.Printf("Seedance candidate downloaded but normalization failed; candidate_count=%d; audit=%s\n", len(urls), filepath.Join(*outDir, "seedance_audit.json"))
			}
			return
		}
		if terminal(lastProviderStatus) {
			audit["status"] = "provider_terminal_no_candidate"
			writeJSON(*outDir, "seedance_audit.json", audit)
			fmt.Printf("Seedance task reached terminal status without a downloadable candidate; provider_status=%s; audit=%s\n", lastProviderStatus, filepath.Join(*outDir, "seedance_audit.json"))
			return
		}
		if polled.Response != nil && polled.Response.Error != nil {
			audit["provider_error_code"] = polled.Response.Error.Code
			audit["provider_error_type"] = polled.Response.Error.Type
			break
		}
	}
	audit["status"] = "provider_pending"
	writeJSON(*outDir, "seedance_audit.json", audit)
	fmt.Printf("Seedance task is still pending; provider_status=%s; audit=%s\n", lastProviderStatus, filepath.Join(*outDir, "seedance_audit.json"))
}

func publicationRequiresRetentionAck(result model.ArkAssetPublicationResult) bool {
	for _, blocker := range result.Blockers {
		if blocker.Code == "tos_retention_client_ack_required" {
			return true
		}
	}
	return false
}

func normalizeCandidateFiles(ctx context.Context, candidateFiles []string, outputDir, ffmpegPath, ffprobePath string, runtime config.AppRuntimeConfig) ([]string, []string, []map[string]any) {
	normalizer := media.FFmpegMiniMaxH3MediaNormalizer{
		FFmpegPath:  firstNonEmpty(ffmpegPath, runtime.FFmpegPath, os.Getenv("CASCADE_FFMPEG_PATH"), "ffmpeg"),
		FFprobePath: firstNonEmpty(ffprobePath, runtime.FFprobePath, os.Getenv("CASCADE_FFPROBE_PATH"), "ffprobe"),
	}
	normalized := []string{}
	errorsFound := []string{}
	probes := []map[string]any{}
	for _, candidate := range candidateFiles {
		if !strings.EqualFold(filepath.Ext(candidate), ".mp4") {
			continue
		}
		source := filepath.Join(outputDir, candidate)
		destination := normalizedCandidatePath(source)
		if _, statErr := os.Stat(destination); statErr == nil {
			if removeErr := os.Remove(destination); removeErr != nil {
				errorsFound = append(errorsFound, fmt.Sprintf("%s: existing normalized artifact could not be replaced: %v", candidate, removeErr))
				continue
			}
		} else if !os.IsNotExist(statErr) {
			errorsFound = append(errorsFound, fmt.Sprintf("%s: normalized artifact destination could not be checked: %v", candidate, statErr))
			continue
		}
		originalProbe, normalizedProbe, err := normalizer.Normalize(ctx, source, destination)
		if err != nil {
			errorsFound = append(errorsFound, fmt.Sprintf("%s: %v", candidate, err))
			continue
		}
		normalized = append(normalized, filepath.Base(destination))
		probes = append(probes, map[string]any{"source_file": filepath.Base(source), "normalized_file": filepath.Base(destination), "original_probe": originalProbe, "normalized_probe": normalizedProbe})
	}
	return normalized, errorsFound, probes
}

func normalizedCandidatePath(source string) string {
	extension := filepath.Ext(source)
	return strings.TrimSuffix(source, extension) + ".normalized.mp4"
}

// candidateArtifactManifest records immutable local evidence for each provider
// download and its normalized derivative. It is review metadata only; it does
// not grant approval or alter a DemoEditPlan.
func candidateArtifactManifest(candidateFiles, normalizedFiles []string, outputDir string) []map[string]any {
	normalizedSet := make(map[string]bool, len(normalizedFiles))
	for _, name := range normalizedFiles {
		normalizedSet[strings.ToLower(filepath.Base(name))] = true
	}
	manifest := make([]map[string]any, 0, len(candidateFiles)+len(normalizedFiles))
	appendArtifact := func(name string, normalized bool) {
		path := filepath.Join(outputDir, name)
		entry := map[string]any{"file": filepath.Base(name), "path": path, "normalized": normalized}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			entry["size_bytes"] = info.Size()
			if digest, err := sha256File(path); err == nil {
				entry["sha256"] = digest
			} else {
				entry["integrity_error"] = err.Error()
			}
		} else if err != nil {
			entry["integrity_error"] = err.Error()
		}
		manifest = append(manifest, entry)
	}
	for _, name := range candidateFiles {
		appendArtifact(name, false)
	}
	for _, name := range normalizedFiles {
		if !normalizedSet[strings.ToLower(filepath.Base(name))] {
			continue
		}
		appendArtifact(name, true)
	}
	return manifest
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func candidateArtifactManifestVerified(manifest []map[string]any) bool {
	if len(manifest) == 0 {
		return false
	}
	for _, entry := range manifest {
		if _, ok := entry["integrity_error"]; ok {
			return false
		}
		if strings.TrimSpace(fmt.Sprint(entry["sha256"])) == "" || entry["size_bytes"] == nil {
			return false
		}
	}
	return true
}

type seedanceReferenceProbe struct {
	DurationSec float64 `json:"duration_sec"`
	VideoCodec  string  `json:"video_codec"`
}

func probeSeedanceReference(ctx context.Context, ffprobePath, source string) (seedanceReferenceProbe, error) {
	result := seedanceReferenceProbe{}
	command := exec.CommandContext(ctx, ffprobePath, "-v", "error", "-show_entries", "format=duration:stream=codec_type,codec_name", "-of", "json", source)
	output, err := command.Output()
	if err != nil {
		return result, fmt.Errorf("source ffprobe failed: %w", err)
	}
	var response struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return result, fmt.Errorf("source ffprobe returned invalid JSON: %w", err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(response.Format.Duration), 64)
	if err != nil || duration <= 0 {
		return result, errors.New("source reference has no valid duration")
	}
	result.DurationSec = duration
	for _, stream := range response.Streams {
		if stream.CodecType == "video" {
			result.VideoCodec = strings.ToLower(strings.TrimSpace(stream.CodecName))
			break
		}
	}
	if result.VideoCodec != "h264" && result.VideoCodec != "hevc" {
		return result, errors.New("source reference video codec must be H.264 or H.265/HEVC")
	}
	if duration < 2 || duration > 15 {
		return result, fmt.Errorf("source reference duration %.3fs is outside the Seedance 2.0 2-15 second limit", duration)
	}
	return result, nil
}

func seedancePreflightSourcePackageID(created time.Time) string {
	return fmt.Sprintf("seedance_preflight_%d", created.UTC().UnixNano())
}

// seedancePreflightRequest deliberately has no execution authority. It keeps
// the existing 2.0 request shape intact while compiling the documented 2.5
// reference-task fields when that exact model is configured.
func seedancePreflightRequest(modelName string, referenceURL string) media.ContentGenerationTaskRequest {
	request := media.ContentGenerationTaskRequest{
		Model: strings.TrimSpace(modelName),
		Content: []media.ContentPart{
			{Type: "text", Text: "Create a concise presentation-only transition from the supplied product recording. Do not invent UI or business actions."},
			{Type: "video_url", VideoURL: &media.MediaURL{URL: strings.TrimSpace(referenceURL)}, Role: "reference_video"},
		},
		Resolution: "1080p", Ratio: "16:9", Duration: 5, GenerateAudio: false, ReturnLastFrame: true, Watermark: false,
	}
	if request.Model == media.Seedance25ServerModel {
		request.OmniReferenceTaskType = "reference"
		request.OutputFormat = "mp4"
	}
	return request
}

// seedancePreflightRequestProfile is audit-safe: it intentionally excludes
// content, source URL, signed query parameters, and credentials.
func seedancePreflightRequestProfile(request media.ContentGenerationTaskRequest) map[string]any {
	return map[string]any{
		"model": request.Model, "omni_reference_task_type": request.OmniReferenceTaskType,
		"resolution": request.Resolution, "ratio": request.Ratio, "duration": request.Duration,
		"generate_audio": request.GenerateAudio, "return_last_frame": request.ReturnLastFrame,
		"watermark": request.Watermark, "output_format": request.OutputFormat,
	}
}

func responseID(value *media.ContentGenerationTaskResponse) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(value.ID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func responseStatus(value *media.ContentGenerationTaskResponse) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(value.Status)
}
func terminal(status string) bool {
	switch strings.ToLower(status) {
	case "succeeded", "success", "completed", "done", "failed", "error", "canceled", "cancelled":
		return true
	}
	return false
}

func extractURLs(response *media.ContentGenerationTaskResponse) []string {
	result := []string{}
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if strings.Contains(strings.ToLower(key), "url") {
					if s, ok := child.(string); ok && strings.HasPrefix(strings.ToLower(s), "http") {
						result = append(result, s)
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	if response != nil {
		walk(response.Output)
	}
	return unique(result)
}
func unique(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}
func download(ctx context.Context, rawURL, destinationBase string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	fatalIf(err)
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	fatalIf(err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fatal(fmt.Errorf("candidate download HTTP %d", resp.StatusCode))
	}
	destination := destinationBase + candidateExtension(resp.Header.Get("Content-Type"), rawURL)
	fatalIf(os.MkdirAll(filepath.Dir(destination), 0o700))
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	fatalIf(err)
	_, err = io.Copy(file, io.LimitReader(resp.Body, 512<<20))
	closeErr := file.Close()
	fatalIf(err)
	fatalIf(closeErr)
	return destination
}
func candidateExtension(contentType, rawURL string) string {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch contentType {
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	}
	parsed, err := url.Parse(rawURL)
	if err == nil {
		switch ext := strings.ToLower(filepath.Ext(parsed.Path)); ext {
		case ".mp4", ".mov", ".webm", ".jpg", ".jpeg", ".png", ".webp":
			return ext
		}
	}
	return ".bin"
}
func writeJSON(dir, name string, value any) {
	fatalIf(os.MkdirAll(dir, 0o700))
	data, err := json.MarshalIndent(value, "", "  ")
	fatalIf(err)
	fatalIf(os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0o600))
}

func readExistingAudit(dir string) (map[string]any, error) {
	path := filepath.Join(dir, "seedance_audit.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var audit map[string]any
	if err := json.Unmarshal(data, &audit); err != nil {
		return nil, fmt.Errorf("decode existing Seedance audit: %w", err)
	}
	if audit == nil || strings.TrimSpace(fmt.Sprint(audit["schema_version"])) != "cascade.seedance_preflight.v1" {
		return nil, errors.New("existing Seedance audit has an unsupported schema")
	}
	return audit, nil
}
func fatalIf(err error) {
	if err != nil {
		fatal(err)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "seedancepreflight failed:", err); os.Exit(1) }
