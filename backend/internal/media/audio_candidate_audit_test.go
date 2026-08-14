package media

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
)

func TestAudioCandidateAuditProvesCandidateWithoutAdoptingIt(t *testing.T) {
	result := AudioModelResult{
		Mode: config.ArkMediaModeReal, Provider: config.ModelProviderDoubao, Model: "seed-tts-2.0", Operation: AudioModelOperationSynthesize,
		Trace: ArkMediaCallTrace{HTTPStatus: 200},
		Response: &AudioModelProviderResponse{TaskID: "task-001", Status: "succeeded", Output: &AudioModelOutput{
			AssetRef: "candidate://tts/abc", LocalPath: `D:\private\candidate.mp3`, MimeType: "audio/mpeg",
			DurationMS: 1750, SampleRateHZ: 24000, Channels: 1, SHA256: strings.Repeat("a", 64),
			Text: "private text", Transcript: []AudioModelTranscriptSegment{{Text: "private transcript"}},
		}},
	}
	audit := NewAudioCandidateAudit(result, time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC))
	if !audit.RealCallMade || !audit.CandidatePresent || !audit.DownloadVerified || !audit.FFprobeVerified {
		t.Fatalf("candidate evidence is incomplete: %+v", audit)
	}
	if audit.ProviderOutputAdopted || audit.InsertedIntoDemoEditPlan || !audit.RequiresExplicitApproval || audit.ReviewState != "candidate_only" {
		t.Fatalf("candidate was unsafely adopted: %+v", audit)
	}
	data, err := json.Marshal(audit)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(data)
	for _, forbidden := range []string{`D:\private`, "private text", "private transcript"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("audit leaked private output: %s", serialized)
		}
	}
}

func TestAudioCandidateAuditDryRunIsNotARealCall(t *testing.T) {
	audit := NewAudioCandidateAudit(AudioModelResult{
		Mode: config.ArkMediaModeDryRun, Provider: config.ModelProviderDoubao, Model: "seed-tts-2.0", Operation: AudioModelOperationSynthesize,
		Request: &AudioModelInput{Text: "must not be copied"},
	}, time.Now())
	if audit.RealCallMade || audit.CandidatePresent || audit.ProviderOutputAdopted || audit.InsertedIntoDemoEditPlan {
		t.Fatalf("dry-run audit is incorrect: %+v", audit)
	}
}
