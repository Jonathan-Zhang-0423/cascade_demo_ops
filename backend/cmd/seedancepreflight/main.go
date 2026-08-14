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
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

func main() {
	source := flag.String("source", "", "local source_reference.mp4")
	outDir := flag.String("output", "artifacts/seedance-preflight/latest", "candidate output directory")
	pollAttempts := flag.Int("poll-attempts", 20, "maximum task polls")
	pollInterval := flag.Duration("poll-interval", 5*time.Second, "delay between polls")
	flag.Parse()
	if strings.TrimSpace(*source) == "" {
		fatal(errors.New("-source is required"))
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
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	pub, err := publisher.PublishArkAssets(ctx, plan, model.DirectorMaterialRef{ID: "seedance_preflight_publication"})
	fatalIf(err)
	if !pub.CanUseForRealCall || len(pub.Items) == 0 || pub.Items[0].ProposedPublicRef == nil {
		writeJSON(*outDir, "publication_result.json", pub)
		fatal(errors.New("TOS publication did not produce a provider-ready URL"))
	}
	ref := pub.Items[0].ProposedPublicRef
	client := media.NewClient(runtime, nil)
	request := media.ContentGenerationTaskRequest{
		Model:      runtime.ModelProviders[config.ModelProviderSeedance].DefaultModel,
		Content:    []media.ContentPart{{Type: "text", Text: "Create a concise presentation-only transition from the supplied product recording. Do not invent UI or business actions."}, {Type: "video_url", VideoURL: &media.MediaURL{URL: ref.URI}, Role: "reference_video"}},
		Resolution: "1080p", Ratio: "16:9", Duration: 5, GenerateAudio: false, ReturnLastFrame: true, Watermark: false,
	}
	created := time.Now().UTC()
	initial, err := client.CreateContentGenerationTask(ctx, request)
	audit := map[string]any{"schema_version": "cascade.seedance_preflight.v1", "created_at": created, "provider": initial.Provider, "model": initial.Model, "real_call_made": initial.Trace.HTTPStatus != 0, "request_trace": initial.Trace, "task_id": responseID(initial.Response), "status": responseStatus(initial.Response), "publication": map[string]any{"mode": pub.Mode, "can_use_for_real_call": pub.CanUseForRealCall, "item_status": pub.Items[0].Status}, "review_state": "candidate_only", "provider_output_adopted": false, "inserted_into_demo_edit_plan": false}
	if err != nil {
		audit["error_class"] = initial.Trace.ErrorClass
		audit["status"] = "provider_call_failed"
		writeJSON(*outDir, "seedance_audit.json", audit)
		fatal(err)
	}
	taskID := responseID(initial.Response)
	if taskID == "" {
		writeJSON(*outDir, "seedance_audit.json", audit)
		fatal(errors.New("Seedance response missing task id"))
	}
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
		audit["status"] = responseStatus(polled.Response)
		if pollErr != nil {
			audit["error_class"] = polled.Trace.ErrorClass
			writeJSON(*outDir, "seedance_audit.json", audit)
			fatal(pollErr)
		}
		urls := extractURLs(polled.Response)
		if len(urls) > 0 {
			for index, raw := range urls {
				download(ctx, raw, filepath.Join(*outDir, fmt.Sprintf("candidate-%02d.mp4", index+1)))
			}
			audit["candidate_url_count"] = len(urls)
			audit["status"] = "candidate_downloaded"
			writeJSON(*outDir, "seedance_audit.json", audit)
			fmt.Printf("Seedance preflight passed; candidate_count=%d; audit=%s\n", len(urls), filepath.Join(*outDir, "seedance_audit.json"))
			return
		}
		if terminal(responseStatus(polled.Response)) {
			break
		}
	}
	audit["status"] = "pending_or_no_candidate"
	writeJSON(*outDir, "seedance_audit.json", audit)
	fmt.Printf("Seedance task completed without downloadable candidate; audit=%s\n", filepath.Join(*outDir, "seedance_audit.json"))
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
	return out
}
func download(ctx context.Context, rawURL, destination string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	fatalIf(err)
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	fatalIf(err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fatal(fmt.Errorf("candidate download HTTP %d", resp.StatusCode))
	}
	fatalIf(os.MkdirAll(filepath.Dir(destination), 0o700))
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	fatalIf(err)
	_, err = io.Copy(file, io.LimitReader(resp.Body, 512<<20))
	closeErr := file.Close()
	fatalIf(err)
	fatalIf(closeErr)
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
