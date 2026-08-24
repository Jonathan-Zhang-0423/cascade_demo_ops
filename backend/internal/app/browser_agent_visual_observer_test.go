package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
)

type browserVisualTestLLM struct {
	task config.ModelTask
	user string
}

type browserVisualFallbackTestLLM struct{ browserVisualTestLLM }

func (*browserVisualFallbackTestLLM) GenerateMultimodal(_ context.Context, task config.ModelTask, _ llm.MultimodalRequest, _ any) (*llm.CallTrace, error) {
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "glm-4.5v", Task: task, ErrorClass: "json_parse_failed"}, llm.ErrDeterministicRequired{Reason: "json_parse_failed"}
}

func (*browserVisualFallbackTestLLM) GenerateMultimodalText(_ context.Context, task config.ModelTask, request llm.MultimodalRequest) (string, *llm.CallTrace, error) {
	if !request.TextMode {
		return "", nil, errors.New("visual fallback did not request text mode")
	}
	return "DECISION=SUCCEEDED\nCONFIDENCE=96%\nSUMMARY=The requested product is complete.\nEVIDENCE_1=Rendered primary surface\nEVIDENCE_2=Two requested controls are visible\nBLOCKING_REASON=NONE", &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "glm-4.5v", Task: task}, nil
}

func (f *browserVisualTestLLM) GenerateMultimodal(_ context.Context, task config.ModelTask, request llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	f.task = task
	f.user = request.User
	*(target.(*browserVisualObservationModelOutput)) = browserVisualObservationModelOutput{Decision: "in_progress", Confidence: .94, Summary: "A build progress surface is visible.", VisibleEvidence: []string{"Building"}}
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "glm-4.5v", Task: task}, nil
}
func (*browserVisualTestLLM) GenerateJSON(context.Context, config.ModelTask, llm.JSONRequest, any) (*llm.CallTrace, error) {
	return nil, nil
}
func (*browserVisualTestLLM) GenerateText(context.Context, config.ModelTask, llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func TestBrowserVisualObserverBridgeBindsLoopbackModel(t *testing.T) {
	client := &browserVisualTestLLM{}
	bridge, err := startBrowserVisualObserverBridge(t.Context(), client, 10, "A responsive interactive product with a rendered primary surface.", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	request := browserVisualObservationRequest{
		SchemaVersion: browserVisualObservationSchemaVersion, ObservationKind: "task_terminal",
		StageID: "stage_8", NodeID: "surface", StageOrder: 8, Sequence: 1,
		SemanticGoal: "Observe the result", ExpectedState: "An interactive result is visible",
		ScreenshotDataURI: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("masked-png")),
	}
	body, _ := json.Marshal(request)
	httpRequest, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, bridge.URL, bytes.NewReader(body))
	httpRequest.Header.Set("Authorization", "Bearer "+bridge.Token)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result browserVisualObservationResponse
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil {
		t.Fatalf("unexpected observer response: status=%d", response.StatusCode)
	}
	if result.Decision != "in_progress" || result.Confidence != .94 || client.task != config.ModelTaskBrowserVisualObservation {
		t.Fatalf("visual task binding was not preserved: result=%+v task=%s", result, client.task)
	}
	if !strings.Contains(client.user, "responsive interactive product") {
		t.Fatalf("expected product summary was not bound into the visual Gate request: %q", client.user)
	}
}

func TestBrowserVisualObservationDoesNotFailOnIncompletePage(t *testing.T) {
	result, err := normalizeBrowserVisualObservation(browserVisualObservationModelOutput{
		Decision: "failed", Confidence: .98, Summary: "The requested result is not visible yet.",
		VisibleEvidence: []string{"Welcome placeholder"}, BlockingReason: "Still waiting",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != "in_progress" || result.BlockingReason != "" {
		t.Fatalf("missing progress became terminal failure: %+v", result)
	}
}

func TestBrowserVisualObserverFailureCodeOnlyExposesSafeClass(t *testing.T) {
	if got := browserVisualObserverFailureCode(&llm.CallTrace{ErrorClass: "http_503"}); got != "observer_model_unavailable_http_503" {
		t.Fatalf("safe provider class was not retained: %q", got)
	}
	if got := browserVisualObserverFailureCode(&llm.CallTrace{ErrorClass: "secret=value"}); got != "observer_model_unavailable" {
		t.Fatalf("unsafe provider detail escaped the observer boundary: %q", got)
	}
}

func TestBrowserVisualObserverUsesBoundedLineFallbackAfterJSONShapeFailure(t *testing.T) {
	client := &browserVisualFallbackTestLLM{}
	bridge, err := startBrowserVisualObserverBridge(t.Context(), client, 10, "A complete interactive product", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	request := browserVisualObservationRequest{
		SchemaVersion: browserVisualObservationSchemaVersion, ObservationKind: "task_terminal",
		StageID: "stage_8", NodeID: "surface", StageOrder: 8, Sequence: 1,
		SemanticGoal: "Observe the result", ExpectedState: "A complete product is visible",
		ScreenshotDataURI: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("masked-png")),
	}
	body, _ := json.Marshal(request)
	httpRequest, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, bridge.URL, bytes.NewReader(body))
	httpRequest.Header.Set("Authorization", "Bearer "+bridge.Token)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result browserVisualObservationResponse
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil {
		t.Fatalf("unexpected fallback response: status=%d", response.StatusCode)
	}
	if result.Decision != "succeeded" || result.ProviderCalls != 2 || len(result.VisibleEvidence) != 2 {
		t.Fatalf("bounded line fallback was not normalized: %+v", result)
	}
}
