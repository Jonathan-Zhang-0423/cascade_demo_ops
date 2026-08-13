// Command ttspreflight performs one non-sensitive Doubao OpenSpeech TTS
// synthesis and media validation. It creates a review-only candidate and
// never inserts the result into a DemoEditPlan or a delivered video.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/media"
)

func main() {
	cwd, err := os.Getwd()
	must(err)
	repoRoot := config.DiscoverDevRepoRoot(cwd)
	must(config.LoadDotEnvFiles(config.DefaultDotEnvPaths(repoRoot)...))
	runtime, err := config.RuntimeConfigFromEnvWithRoot(repoRoot)
	must(err)
	if runtime.ArkMediaMode != config.ArkMediaModeReal {
		must(fmt.Errorf("CASCADE_ARK_MEDIA_MODE must be real for TTS preflight"))
	}
	text := strings.TrimSpace(os.Getenv("CASCADE_TTS_PREFLIGHT_TEXT"))
	if text == "" {
		text = "Cascade 配音链路测试。"
	}
	if len([]rune(text)) > 80 {
		must(fmt.Errorf("CASCADE_TTS_PREFLIGHT_TEXT must not exceed 80 characters"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := media.NewAudioClient(runtime, nil).Process(ctx, media.AudioModelInput{
		Operation:    media.AudioModelOperationSynthesize,
		Text:         text,
		VoiceID:      strings.TrimSpace(os.Getenv("VOLC_TTS_SPEAKER")),
		OutputFormat: "mp3",
		SampleRateHZ: 24000,
	})
	must(err)
	if result.Response == nil || result.Response.Output == nil {
		must(fmt.Errorf("TTS preflight completed without a candidate output"))
	}
	output := result.Response.Output
	fmt.Println("TTS preflight passed")
	fmt.Printf("provider=%s\n", result.Provider)
	fmt.Printf("resource_id=%s\n", result.Model)
	fmt.Printf("status=%s\n", result.Response.Status)
	fmt.Printf("candidate_path=%s\n", output.LocalPath)
	fmt.Printf("mime_type=%s\n", output.MimeType)
	fmt.Printf("duration_ms=%d\n", output.DurationMS)
	fmt.Printf("sample_rate_hz=%d\n", output.SampleRateHZ)
	fmt.Printf("channels=%d\n", output.Channels)
	fmt.Printf("sha256=%s\n", output.SHA256)
	fmt.Println("review_state=candidate_only")
	fmt.Println("inserted_into_demo_edit_plan=false")
}

func must(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "TTS preflight failed:", err)
	os.Exit(1)
}
