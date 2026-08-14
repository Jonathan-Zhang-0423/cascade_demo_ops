// Command seedancepreflight performs one small, explicitly authorized real
// Seedance task using a selected local reference asset. It never changes an
// edit plan and writes only redacted task/output metadata.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

func main() {
	source := flag.String("source", "", "local source_reference.mp4")
	taskIDFlag := flag.String("task-id", "", "resume polling an existing Seedance task without creating a new task")
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
	audit := map[string]any{"schema_version": "cascade.seedance_preflight.v1", "created_at": created, "provider": config.ModelProviderSeedance, "model": runtime.ModelProviders[config.ModelProviderSeedance].DefaultModel, "real_call_made": false, "review_state": "candidate_only", "provider_output_adopted": false, "inserted_into_demo_edit_plan": false}
	if taskID == "" {
		info, err := os.Stat(*source)
		fatalIf(err)
		if info.IsDir() {
			fatal(errors.New("source must be a file"))
		}
		plan := model.ArkAssetPublicationPlan{
			SchemaVersion: model.ArkAssetPublicationPlanSchemaVersion, PlanID: "seedance_preflight_publication",
			CreatedAt: time.Now().UTC(), Mode: "real_preflight", SourcePackageID: "seedance_preflight",
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
			fatal(errors.New("TOS publication did not produce a provider-ready URL"))
		}
		ref := pub.Items[0].ProposedPublicRef
		request := media.ContentGenerationTaskRequest{
			Model:      runtime.ModelProviders[config.ModelProviderSeedance].DefaultModel,
			Content:    []media.ContentPart{{Type: "text", Text: "Create a concise presentation-only transition from the supplied product recording. Do not invent UI or business actions."}, {Type: "video_url", VideoURL: &media.MediaURL{URL: ref.URI}, Role: "reference_video"}},
			Resolution: "1080p", Ratio: "16:9", Duration: 5, GenerateAudio: false, ReturnLastFrame: true, Watermark: false,
		}
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
	} else {
		audit["task_id"] = taskID
		audit["task_resumed"] = true
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
			audit["candidate_url_count"] = len(urls)
			audit["candidate_files"] = candidateFiles
			audit["status"] = "candidate_downloaded"
			writeJSON(*outDir, "seedance_audit.json", audit)
			fmt.Printf("Seedance preflight passed; candidate_count=%d; audit=%s\n", len(urls), filepath.Join(*outDir, "seedance_audit.json"))
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

func responseID(value *media.ContentGenerationTaskResponse) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(value.ID)
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
func fatalIf(err error) {
	if err != nil {
		fatal(err)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "seedancepreflight failed:", err); os.Exit(1) }
