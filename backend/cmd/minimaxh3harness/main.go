// Command minimaxh3harness performs one explicitly authorized MiniMax-H3
// presentation-shot run. It loads local development credentials, optionally
// enriches the prompt with H3-Context-IR, creates one video task, downloads and
// normalizes the output, and stops before human content review or edit-plan use.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
)

func main() {
	prompt := flag.String("prompt", "Create a restrained abstract software-product intro with subtle depth, clean cinematic lighting, no product UI, no logos, no claims, and no readable text.", "presentation-only H3 prompt")
	intentID := flag.String("intent-id", "operator_intro", "stable presentation intent id")
	purpose := flag.String("purpose", media.GeneratedShotPurposeIntro, "intro, outro, section_divider, abstract_b_roll, brand_atmosphere, or transition")
	outputDir := flag.String("output", "artifacts/minimax-h3-harness/latest", "harness artifact directory")
	resolution := flag.String("resolution", "768P", "768P or 2K")
	duration := flag.Int("duration", 4, "generated duration in seconds (4-15)")
	ratio := flag.String("ratio", "16:9", "output aspect ratio")
	useContextIR := flag.Bool("context-ir", true, "run H3-Context-IR before video generation")
	taskID := flag.String("task-id", "", "resume an existing H3 generation task without creating another charged task")
	pollAttempts := flag.Int("poll-attempts", 120, "maximum generation polls")
	pollInterval := flag.Duration("poll-interval", 5*time.Second, "delay between provider polls")
	timeout := flag.Duration("timeout", 15*time.Minute, "whole harness timeout")
	flag.Parse()

	if strings.TrimSpace(*prompt) == "" {
		fatal(errors.New("-prompt is required"))
	}
	if *pollAttempts < 1 || *pollAttempts > 240 {
		fatal(errors.New("-poll-attempts must be between 1 and 240"))
	}
	cwd, err := os.Getwd()
	fatalIf(err)
	repoRoot := config.DiscoverDevRepoRoot(cwd)
	fatalIf(config.LoadDotEnvFiles(config.DefaultDotEnvPaths(repoRoot)...))
	harnessConfig, err := media.LoadMiniMaxH3HarnessConfig(os.Getenv)
	fatalIf(err)
	runtimeConfig, err := config.RuntimeConfigFromEnvWithRoot(repoRoot)
	fatalIf(err)

	client := media.NewMiniMaxH3Client(media.MiniMaxH3ClientOptions{
		Mode: harnessConfig.Mode, APIKey: harnessConfig.APIKey, BaseURL: harnessConfig.BaseURL,
	})
	intent := media.GeneratedShotIntent{
		IntentID: strings.TrimSpace(*intentID), Purpose: strings.TrimSpace(*purpose), Prompt: strings.TrimSpace(*prompt),
		Required: false, DurationSec: *duration, AspectRatio: strings.TrimSpace(*ratio),
		ContentPolicy: media.GeneratedShotContentPolicy{
			PresentationOnly: true, MayRepresentBusinessStep: false, MayReplaceCapturedUI: false, RequiresExplicitReview: true,
		},
		FailurePolicy: media.GeneratedShotFailureContinue,
	}
	absoluteOutput, err := filepath.Abs(*outputDir)
	fatalIf(err)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, runErr := media.RunMiniMaxH3Harness(ctx, intent, media.MiniMaxH3HarnessOptions{
		Client: client, OutputDir: absoluteOutput, Resolution: *resolution, UseContextIR: *useContextIR,
		ContextIRPollAttempts: *pollAttempts, ContextIRPollInterval: *pollInterval,
		GenerationPollAttempts: *pollAttempts, GenerationPollInterval: *pollInterval, Timeout: *timeout,
		ExistingGenerationTaskID: strings.TrimSpace(*taskID),
		Normalizer: media.FFmpegMiniMaxH3MediaNormalizer{
			FFmpegPath: runtimeConfig.FFmpegPath, FFprobePath: runtimeConfig.FFprobePath,
		},
		AllowUnpricedOperatorSubmit: true,
	})
	fatalIf(os.MkdirAll(absoluteOutput, 0o700))
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
	fmt.Printf("MiniMax-H3 harness completed; status=%s; task_id=%s; normalized=%s; audit=%s\n",
		result.Status, result.GenerationTaskID, result.Candidate.NormalizedArtifact.Path, auditPath)
}

func fatalIf(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "minimaxh3harness failed:", err)
	os.Exit(1)
}
