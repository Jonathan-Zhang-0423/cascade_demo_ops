// Command seedance25harness performs one explicitly authorized Seedance 2.5
// FinalFilm candidate run. It loads credentials from local dotenv files,
// submits through the production adapter, downloads and normalizes with
// FFmpeg, and stops before human content review or editor use.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
)

func main() {
	authorized := flag.Bool("authorize-real-call", false, "required acknowledgement that this run creates a billable Seedance 2.5 task")
	prompt := flag.String("prompt", "Create a restrained abstract software-product transition with subtle depth, clean cinematic lighting, no product UI, no logos, no claims, no people, and no readable text.", "presentation-only prompt")
	intentID := flag.String("intent-id", "operator_seedance25_intro", "stable presentation intent ID")
	outputDir := flag.String("output", "artifacts/seedance-2.5-harness/latest", "harness artifact directory")
	taskID := flag.String("task-id", "", "resume an existing Seedance 2.5 task without creating another billable task")
	duration := flag.Int("duration", 4, "generated duration in seconds (4-15)")
	pollAttempts := flag.Int("poll-attempts", 180, "maximum task polls")
	pollInterval := flag.Duration("poll-interval", 5*time.Second, "delay between provider polls")
	timeout := flag.Duration("timeout", 20*time.Minute, "whole harness timeout")
	flag.Parse()

	if !*authorized {
		fatal(errors.New("-authorize-real-call is required"))
	}
	if strings.TrimSpace(*prompt) == "" {
		fatal(errors.New("-prompt is required"))
	}
	if *duration < 4 || *duration > 15 {
		fatal(errors.New("-duration must be between 4 and 15 seconds"))
	}
	if *pollAttempts < 1 || *pollAttempts > 240 {
		fatal(errors.New("-poll-attempts must be between 1 and 240"))
	}

	cwd, err := os.Getwd()
	fatalIf(err)
	repoRoot := config.DiscoverDevRepoRoot(cwd)
	fatalIf(config.LoadDotEnvFiles(config.DefaultDotEnvPaths(repoRoot)...))
	runtime, err := config.RuntimeConfigFromEnvWithRoot(repoRoot)
	fatalIf(err)
	credential := runtime.ModelProviders[config.ModelProviderSeedance]
	if runtime.ArkMediaMode != config.ArkMediaModeReal {
		fatal(errors.New("CASCADE_ARK_MEDIA_MODE must be real"))
	}
	if !credential.Enabled || credential.DefaultModel != media.Seedance25ServerModel {
		fatal(errors.New("Seedance credentials must be configured with SEEDANCE_MODEL=doubao-seedance-2-5-260628"))
	}
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("CASCADE_SEEDANCE_FINAL_FILM_ENABLED")), "true") {
		fatal(errors.New("CASCADE_SEEDANCE_FINAL_FILM_ENABLED must be true"))
	}

	absoluteOutput, err := filepath.Abs(*outputDir)
	fatalIf(err)
	fatalIf(os.MkdirAll(absoluteOutput, 0o700))
	adapter, err := media.NewSeedance25ProviderAdapter(media.Seedance25ProviderAdapterOptions{
		Enabled: true,
		Client:  media.NewClient(runtime, nil),
		Downloader: media.HTTPMiniMaxH3OutputDownloader{
			Client: http.DefaultClient,
		},
		Normalizer: media.FFmpegMiniMaxH3MediaNormalizer{
			FFmpegPath: runtime.FFmpegPath, FFprobePath: runtime.FFprobePath,
		},
		PollAttempts: *pollAttempts, PollInterval: *pollInterval,
	})
	fatalIf(err)
	intent := media.GeneratedShotIntent{
		IntentID: strings.TrimSpace(*intentID), Purpose: media.GeneratedShotPurposeIntro,
		Prompt: strings.TrimSpace(*prompt), Required: false, DurationSec: *duration, AspectRatio: "16:9",
		ContentPolicy: media.GeneratedShotContentPolicy{
			PresentationOnly: true, MayRepresentBusinessStep: false, MayReplaceCapturedUI: false, RequiresExplicitReview: true,
		},
		FailurePolicy: media.GeneratedShotFailureContinue,
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	runID := time.Now().UTC().Format("20060102T150405.000000000Z")
	result, runErr := adapter.Execute(ctx, media.GeneratedShotProviderExecutionRequest{
		Intent: intent, GenerationAuthorized: true, AuthorizationRef: "operator://seedance25harness/" + runID,
		IdempotencyKey: "seedance25harness:" + strings.TrimSpace(*intentID) + ":" + runID,
		AdmissionScope: "operator:seedance25harness", OutputDir: absoluteOutput, Timeout: *timeout,
		ResumeProviderTaskID: strings.TrimSpace(*taskID),
	})
	auditPath := filepath.Join(absoluteOutput, "harness_result.json")
	data, marshalErr := json.MarshalIndent(result, "", "  ")
	fatalIf(marshalErr)
	fatalIf(os.WriteFile(auditPath, append(data, '\n'), 0o600))
	if runErr != nil {
		fatal(fmt.Errorf("%w; audit=%s", runErr, auditPath))
	}
	if result.Candidate == nil || result.StructuralReview == nil {
		fatal(fmt.Errorf("harness completed without a reviewable candidate; audit=%s", auditPath))
	}
	fmt.Printf("Seedance 2.5 harness completed; status=%s; task_id=%s; normalized=%s; audit=%s\n",
		result.Status, result.ProviderTaskID, result.Candidate.NormalizedArtifact.Path, auditPath)
}

func fatalIf(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "seedance25harness failed:", err)
	os.Exit(1)
}
