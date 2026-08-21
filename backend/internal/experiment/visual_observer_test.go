package experiment

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
)

type visualObserverLLM struct {
	request llm.MultimodalRequest
	output  TemporalVisualObservation
}

func (f *visualObserverLLM) GenerateJSON(context.Context, config.ModelTask, llm.JSONRequest, any) (*llm.CallTrace, error) {
	return nil, nil
}
func (f *visualObserverLLM) GenerateText(context.Context, config.ModelTask, llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}
func (f *visualObserverLLM) GenerateMultimodal(_ context.Context, task config.ModelTask, request llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	f.request = request
	if task != config.ModelTaskBrowserVisualObservation {
		panic("unexpected model task")
	}
	output := target.(*TemporalVisualObservation)
	*output = f.output
	return &llm.CallTrace{Provider: config.ModelProviderMinimax, Model: "visual-test"}, nil
}

func TestTemporalVisualObserverPersistsBoundedArtifactAndUsesOnlyArtifactPair(t *testing.T) {
	root := t.TempDir()
	previousPath := filepath.Join(root, "previous.png")
	currentPath := filepath.Join(root, "current.png")
	if err := os.WriteFile(previousPath, []byte("png-previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath, []byte("png-current"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &visualObserverLLM{output: TemporalVisualObservation{
		Phase: "preview_ready", Transition: "preview_ready", Decision: "succeed", Confidence: 0.96,
		Criteria: []VisualCriterionResult{
			{CriterionID: "visible_result", Status: "met", EvidenceRefs: []string{"artifact-current"}},
			{CriterionID: "state_transition", Status: "met", EvidenceRefs: []string{"artifact-previous", "artifact-current"}},
		},
		VisibleEvidence: []string{"A stable interactive result surface is visible."}, EvidenceKinds: []string{"visual", "dom"}, MaterialChanged: true,
	}}
	observer, err := NewTemporalVisualObserver(client, root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := observer.Observe(t.Context(), VisualObservationRequest{
		RunID: "run-1", LegID: "main", Sequence: 2, ElapsedMS: 60_000, CurrentPhase: "preview_candidate",
		AllowedNextPhases: []string{"preview_ready"}, PreviousArtifactPath: previousPath, PreviousArtifactRef: "artifact-previous",
		CurrentArtifactPath: currentPath, CurrentArtifactRef: "artifact-current", Plan: visualObserverPlan(),
		ObservedEvidenceKinds: []string{"visual", "dom"}, MaterialChanged: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ArtifactRef.Role != "temporal_visual_observation" || len(client.request.Images) != 2 {
		t.Fatalf("unexpected result or image pair: %+v images=%d", result, len(client.request.Images))
	}
	if strings.Contains(client.request.User, previousPath) || strings.Contains(client.request.User, currentPath) || strings.Contains(client.request.System, "selector") && !strings.Contains(client.request.System, "never") {
		t.Fatalf("model prompt leaked a path or enabled selector memory: %s", client.request.User)
	}
	artifactPath := filepath.Join(root, "experiments", "run-1", "main", "visual-observations", "observation-002.json")
	if info, statErr := os.Stat(artifactPath); statErr != nil || info.Size() > MaxEventBodyBytes {
		t.Fatalf("observation artifact was not persisted within bounds: info=%+v err=%v", info, statErr)
	}
}

func TestTemporalVisualObservationRejectsSingleChannelSuccessAndUnknownArtifact(t *testing.T) {
	request := VisualObservationRequest{
		RunID: "run", LegID: "leg", Sequence: 1, CurrentPhase: "preview_candidate", AllowedNextPhases: []string{"preview_ready"},
		CurrentArtifactPath: "current.png", CurrentArtifactRef: "current", Plan: visualObserverPlan(),
	}
	for name, observation := range map[string]TemporalVisualObservation{
		"single channel":   {Phase: "preview_ready", Transition: "preview_ready", Decision: "succeed", Confidence: 1, Criteria: []VisualCriterionResult{{CriterionID: "visual", Status: "met", EvidenceRefs: []string{"current"}}}, EvidenceKinds: []string{"visual"}},
		"foreign artifact": {Phase: "preview_candidate", Transition: "preview_candidate", Decision: "continue", Confidence: .7, Criteria: []VisualCriterionResult{{CriterionID: "pending", Status: "unknown", EvidenceRefs: []string{"foreign"}}}, EvidenceKinds: []string{"visual"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateTemporalVisualObservation(observation, request); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestTemporalVisualObserverRejectsImageOutsideArtifactRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	observer, err := NewTemporalVisualObserver(&visualObserverLLM{}, root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = observer.Observe(t.Context(), VisualObservationRequest{RunID: "run", LegID: "main", Sequence: 1, CurrentPhase: "request_submitted", AllowedNextPhases: []string{"plan_ready"}, CurrentArtifactPath: outside, CurrentArtifactRef: "outside", Plan: visualObserverPlan()})
	if err == nil || !strings.Contains(err.Error(), "escapes artifact root") {
		t.Fatalf("expected artifact-root failure, got %v", err)
	}
}

func visualObserverPlan() ObservationPlan {
	return ObservationPlan{
		SchemaVersion: ObservationPlanSchemaVersion, InitialPhase: "request_submitted", TerminalPhase: "preview_ready", MaxVisualCalls: 12, RequiredTerminalChannels: 2,
		Transitions: []ObservationTransition{{From: "preview_candidate", To: "preview_ready", EvidenceKinds: []string{"visual", "dom"}}},
	}
}
