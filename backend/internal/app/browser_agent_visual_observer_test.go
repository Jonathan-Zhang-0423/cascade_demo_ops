package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
)

type fakeBrowserVisualObservationLLM struct {
	task    config.ModelTask
	request llm.MultimodalRequest
}

func (f *fakeBrowserVisualObservationLLM) GenerateMultimodal(_ context.Context, task config.ModelTask, request llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	f.task, f.request = task, request
	output := target.(*browserVisualObservationModelOutput)
	*output = browserVisualObservationModelOutput{Decision: "in_progress", Confidence: 0.94, Summary: "A build progress surface is visible.", VisibleEvidence: []string{"Building 72%"}}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "glm-4.5v", Task: task, AdapterVersion: "test"}, nil
}

func (*fakeBrowserVisualObservationLLM) GenerateJSON(context.Context, config.ModelTask, llm.JSONRequest, any) (*llm.CallTrace, error) {
	return nil, nil
}

func (*fakeBrowserVisualObservationLLM) GenerateText(context.Context, config.ModelTask, llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func TestBrowserVisualObserverBridgeBindsLoopbackModelAndProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &fakeBrowserVisualObservationLLM{}
	progressCalled := false
	bridge, err := startBrowserVisualObserverBridge(ctx, client, 10, func(stage, _ string, percent int) {
		progressCalled = stage == "browser_visual_observation" && percent > 40
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	request := browserVisualObservationRequest{
		SchemaVersion: browserVisualObservationSchemaVersion,
		RunID:         "run_1", StageID: "stage_7", NodeID: "final_observe", StageOrder: 7, Sequence: 1,
		ElapsedMS: 60_000, SemanticGoal: "Observe completion", ExpectedState: "The completed result is visible",
		ScreenshotDataURI: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("masked-png")),
	}
	body, _ := json.Marshal(request)
	httpRequest, err := http.NewRequestWithContext(t.Context(), http.MethodPost, bridge.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+bridge.Token)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var result browserVisualObservationResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Decision != "in_progress" || result.Confidence != 0.94 || result.ModelTrace["model"] != "glm-4.5v" {
		t.Fatalf("unexpected observation: %+v", result)
	}
	if client.task != config.ModelTaskBrowserVisualObservation || len(client.request.Images) != 1 || !progressCalled {
		t.Fatalf("model binding not preserved: task=%s images=%d progress=%v", client.task, len(client.request.Images), progressCalled)
	}
}

func TestBrowserVisualObserverRejectsMissingBearer(t *testing.T) {
	bridge, err := startBrowserVisualObserverBridge(t.Context(), &fakeBrowserVisualObservationLLM{}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	response, err := http.Post(bridge.URL, "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestBrowserVisualObservationAcceptsProviderAnswerEnvelope(t *testing.T) {
	var output browserVisualObservationModelOutput
	if err := json.Unmarshal([]byte(`{"answer":{"decision":"unknown","confidence":"0.85","summary":"Only a project list is visible.","visible_evidence":["My Projects"],"blocking_reason":"No finished preview."}}`), &output); err != nil {
		t.Fatal(err)
	}
	if output.Decision != "unknown" || output.Confidence != 0.85 || output.Summary == "" || len(output.VisibleEvidence) != 1 {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestBrowserVisualObservationAcceptsProviderStringAnswerEnvelope(t *testing.T) {
	var output browserVisualObservationModelOutput
	if err := json.Unmarshal([]byte(`{"answer":"{\"decision\":\"failed\",\"confidence\":\"0.96\",\"summary\":\"The control is checked.\",\"visible_evidence\":[\"filled blue square\"],\"blocking_reason\":\"\"}"}`), &output); err != nil {
		t.Fatal(err)
	}
	if output.Decision != "failed" || output.Confidence != 0.96 || output.Summary == "" {
		t.Fatalf("unexpected string answer output: %+v", output)
	}
}

func TestBrowserVisualObservationAcceptsQualitativeConfidenceAndScalarEvidence(t *testing.T) {
	var output browserVisualObservationModelOutput
	if err := json.Unmarshal([]byte(`{"decision":"failed","confidence":"high","summary":"The control is checked.","visible_evidence":"Blue box with a white checkmark","blocking_reason":null}`), &output); err != nil {
		t.Fatal(err)
	}
	if output.Decision != "failed" || output.Confidence != 0.95 || len(output.VisibleEvidence) != 1 {
		t.Fatalf("unexpected qualitative output: %+v", output)
	}
}

func TestBrowserVisualObservationDoesNotTreatIncompletePollAsTerminalFailure(t *testing.T) {
	response, err := normalizeBrowserVisualObservation(browserVisualObservationModelOutput{
		Decision: "failed", Confidence: 0.95, Summary: "The result is not complete yet.",
		VisibleEvidence: []string{"Welcome! Start building your project here.", "No completed preview is visible."},
		BlockingReason:  "Only sixty seconds have elapsed.",
	}, &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "glm-4.5v"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Decision != "in_progress" || response.BlockingReason != "" {
		t.Fatalf("an incomplete poll must remain non-terminal: %+v", response)
	}
}

func TestBrowserVisualObservationKeepsExplicitVisibleTerminalFailure(t *testing.T) {
	response, err := normalizeBrowserVisualObservation(browserVisualObservationModelOutput{
		Decision: "failed", Confidence: 0.97, Summary: "The build failed.",
		VisibleEvidence: []string{"Build failed: permission denied"}, BlockingReason: "Visible terminal error.",
	}, &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "glm-4.5v"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Decision != "failed" || response.BlockingReason == "" {
		t.Fatalf("an explicit visible terminal error must remain failed: %+v", response)
	}
}

func TestBrowserVisualToggleObservationKeepsClearlyOppositeState(t *testing.T) {
	response, err := normalizeBrowserVisualObservationForKind(browserVisualObservationModelOutput{
		Decision: "failed", Confidence: 0.98, Summary: "The cropped control is checked.",
		VisibleEvidence: []string{"Blue filled square with a visible check mark"},
	}, &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "glm-4.5v"}, "toggle_state")
	if err != nil {
		t.Fatal(err)
	}
	if response.Decision != "failed" {
		t.Fatalf("an opposite visual toggle state must remain failed: %+v", response)
	}
}

func TestBrowserVisualToggleRequestRequiresExpectedBoolean(t *testing.T) {
	request := browserVisualObservationRequest{
		SchemaVersion: browserVisualObservationSchemaVersion, ObservationKind: "toggle_state",
		StageID: "stage_1", NodeID: "mode", StageOrder: 1, Sequence: 1,
		SemanticGoal: "Set direct mode", ExpectedState: "unchecked",
		ScreenshotDataURI: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png")),
	}
	if err := validateBrowserVisualObservationRequest(request); err == nil || err.Error() != "observer_expected_boolean_missing" {
		t.Fatalf("missing expected boolean must be rejected, got %v", err)
	}
}
