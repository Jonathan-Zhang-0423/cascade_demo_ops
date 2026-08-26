package media

import (
	"testing"
)

func validGeneratedShotIntent() GeneratedShotIntent {
	return GeneratedShotIntent{
		IntentID: "shot_001", Purpose: GeneratedShotPurposeIntro,
		Prompt:   "Create an abstract presentation-only opening transition without product UI.",
		Required: false, DurationSec: 5, AspectRatio: "16:9",
		ContentPolicy: GeneratedShotContentPolicy{
			PresentationOnly: true, MayRepresentBusinessStep: false, MayReplaceCapturedUI: false, RequiresExplicitReview: true,
		},
		FailurePolicy: GeneratedShotFailureContinue,
	}
}

func TestGeneratedShotIntentRejectsAuthoritativeOrRequiredUse(t *testing.T) {
	intent := validGeneratedShotIntent()
	intent.Required = true
	if err := ValidateGeneratedShotIntent(intent); err == nil {
		t.Fatal("expected required generated shot to be rejected")
	}
	intent = validGeneratedShotIntent()
	intent.ContentPolicy.MayReplaceCapturedUI = true
	if err := ValidateGeneratedShotIntent(intent); err == nil {
		t.Fatal("expected UI replacement to be rejected")
	}
}

func TestGeneratedShotCompilersProduceIndependentProviderRequests(t *testing.T) {
	intent := validGeneratedShotIntent()
	intent.References = []GeneratedShotReference{{
		ArtifactID: "asset_1", URI: "https://assets.example.test/reference.png", MimeType: "image/png", Usage: GeneratedShotReferenceGeneral,
	}}
	seedance, err := (Seedance20GeneratedShotCompiler{}).Compile(intent)
	if err != nil {
		t.Fatal(err)
	}
	h3, err := (MiniMaxH3GeneratedShotCompiler{}).Compile(intent)
	if err != nil {
		t.Fatal(err)
	}
	if !seedance.DryRunOnly || !h3.DryRunOnly {
		t.Fatalf("compiled requests must remain dry-run: seedance=%+v h3=%+v", seedance, h3)
	}
	if seedance.Model != Seedance20ServerModel || seedance.Request.Resolution != "1080p" || seedance.Request.Content[1].Role != "" {
		t.Fatalf("seedance request = %+v", seedance.Request)
	}
	if h3.Model != MiniMaxH3Model || h3.Request.Resolution != "2K" || h3.Request.Content[1].Role != "reference_image" {
		t.Fatalf("h3 request = %+v", h3.Request)
	}
	if seedance.Request.Model == h3.Request.Model || seedance.Request.Resolution == h3.Request.Resolution {
		t.Fatal("provider request structures were unexpectedly conflated")
	}
}

func TestSeedance25CompilerUsesOfficialReferenceFieldsWithoutEnablingCalls(t *testing.T) {
	intent := validGeneratedShotIntent()
	intent.References = []GeneratedShotReference{
		{ArtifactID: "image_1", URI: "https://bucket.tos-cn-beijing.ivolces.com/reference.png?signature=redacted", MimeType: "image/png", Usage: GeneratedShotReferenceGeneral},
		{ArtifactID: "video_1", URI: "https://bucket.tos-cn-beijing.ivolces.com/reference.mp4?signature=redacted", MimeType: "video/mp4", Usage: GeneratedShotReferenceGeneral},
	}
	compiled, err := (Seedance25GeneratedShotCompiler{}).Compile(intent)
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.DryRunOnly || compiled.Provider != GeneratedShotProviderSeedance25 || compiled.Model != Seedance25ServerModel {
		t.Fatalf("compiled safety envelope = %+v", compiled)
	}
	request := compiled.Request
	if request.OmniReferenceTaskType != "reference" || request.OutputFormat != "mp4" || request.Resolution != "1080p" || request.GenerateAudio || request.Watermark || !request.ReturnLastFrame {
		t.Fatalf("Seedance 2.5 request = %+v", request)
	}
	if len(request.Content) != 3 || request.Content[1].Role != "reference_image" || request.Content[1].ImageURL == nil || request.Content[2].Role != "reference_video" || request.Content[2].VideoURL == nil {
		t.Fatalf("Seedance 2.5 content = %+v", request.Content)
	}
}

func TestSeedance25CompilerLocksFrameModeToAdaptive(t *testing.T) {
	intent := validGeneratedShotIntent()
	intent.AspectRatio = "16:9"
	intent.References = []GeneratedShotReference{{
		ArtifactID: "first", URI: "https://assets.example.test/first.png", MimeType: "image/png", Usage: GeneratedShotReferenceFirstFrame,
	}}
	compiled, err := (Seedance25GeneratedShotCompiler{}).Compile(intent)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Request.Ratio != "adaptive" || compiled.Request.OmniReferenceTaskType != "" || compiled.Request.Content[1].Role != "first_frame" {
		t.Fatalf("Seedance 2.5 frame request = %+v", compiled.Request)
	}
}

func TestSeedance25CompilerOmitsReferenceTaskTypeForTextOnly(t *testing.T) {
	intent := validGeneratedShotIntent()
	intent.References = nil
	compiled, err := (Seedance25GeneratedShotCompiler{}).Compile(intent)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Request.OmniReferenceTaskType != "" {
		t.Fatalf("text-only request unexpectedly set omni_reference_task_type=%q", compiled.Request.OmniReferenceTaskType)
	}
}

func TestGeneratedShotProviderProfilesRejectDifferentBoundaries(t *testing.T) {
	vertical := validGeneratedShotIntent()
	vertical.AspectRatio = "9:16"
	if _, err := (Seedance20GeneratedShotCompiler{}).Compile(vertical); err == nil {
		t.Fatal("expected current Seedance project profile to reject vertical ratio")
	}
	if _, err := (MiniMaxH3GeneratedShotCompiler{}).Compile(vertical); err != nil {
		t.Fatalf("expected H3 profile to accept vertical ratio: %v", err)
	}

	frame := validGeneratedShotIntent()
	frame.References = []GeneratedShotReference{{
		ArtifactID: "first", URI: "https://assets.example.test/first.png", MimeType: "image/png", Usage: GeneratedShotReferenceFirstFrame,
	}}
	if _, err := (Seedance20GeneratedShotCompiler{}).Compile(frame); err == nil {
		t.Fatal("expected current Seedance project profile to reject frame mode")
	}
	h3, err := (MiniMaxH3GeneratedShotCompiler{}).Compile(frame)
	if err != nil {
		t.Fatalf("expected H3 profile to accept first frame: %v", err)
	}
	if h3.Request.Ratio != "adaptive" || h3.Request.Content[1].Role != "first_frame" {
		t.Fatalf("h3 frame request = %+v", h3.Request)
	}
}

func TestGeneratedShotProfilesRejectLastFrameOnlyBeforeCompilation(t *testing.T) {
	intent := validGeneratedShotIntent()
	intent.References = []GeneratedShotReference{{
		ArtifactID: "last", URI: "https://assets.example.test/last.png", MimeType: "image/png", Usage: GeneratedShotReferenceLastFrame,
	}}
	for name, compiler := range map[string]GeneratedShotProviderCompiler{
		"seedance-2.0": Seedance20GeneratedShotCompiler{},
		"seedance-2.5": Seedance25GeneratedShotCompiler{},
		"h3":           MiniMaxH3GeneratedShotCompiler{},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := compiler.Compile(intent); err == nil {
				t.Fatal("expected last-frame-only intent to be rejected")
			}
		})
	}
}

func TestGeneratedShotCommonProfileRejectsUnsafeReference(t *testing.T) {
	intent := validGeneratedShotIntent()
	intent.References = []GeneratedShotReference{{
		ArtifactID: "asset_1", URI: "file:///tmp/reference.png", MimeType: "image/png", Usage: GeneratedShotReferenceGeneral,
	}}
	if err := ValidateGeneratedShotIntent(intent); err == nil {
		t.Fatal("expected local file URI to be rejected")
	}
}
