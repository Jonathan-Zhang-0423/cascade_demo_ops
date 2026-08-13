package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTTSPreflightDoesNotContainCredentialOrDeliveryMutationCode(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("main.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, forbidden := range []string{"VOLC_TTS_ACCESS_KEY)", "Authorization", "DemoEditPlan{", "narrations = append", "tts_confirmed"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("preflight must not read/print credentials or mutate delivery plans: found %q", forbidden)
		}
	}
	for _, required := range []string{"candidate_only", "inserted_into_demo_edit_plan=false", "media.NewAudioClient"} {
		if !strings.Contains(source, required) {
			t.Fatalf("preflight is missing review-only guard %q", required)
		}
	}
}
