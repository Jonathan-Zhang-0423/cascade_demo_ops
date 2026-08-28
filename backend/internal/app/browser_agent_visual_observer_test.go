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
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
)

type browserVisualTestLLM struct {
	task config.ModelTask
	user string
}

type browserVisualFallbackTestLLM struct {
	browserVisualTestLLM
	multimodalCalls int
	textCalls       int
}

type blockingBrowserVisualTestLLM struct {
	browserVisualTestLLM
	release chan struct{}
}

func (f *blockingBrowserVisualTestLLM) GenerateMultimodal(_ context.Context, task config.ModelTask, request llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	<-f.release
	return f.browserVisualTestLLM.GenerateMultimodal(context.Background(), task, request, target)
}

func (f *browserVisualFallbackTestLLM) GenerateMultimodal(_ context.Context, task config.ModelTask, _ llm.MultimodalRequest, _ any) (*llm.CallTrace, error) {
	f.multimodalCalls++
	return &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "glm-4.5v", Task: task, ErrorClass: "json_parse_failed"}, llm.ErrDeterministicRequired{Reason: "json_parse_failed"}
}

func (f *browserVisualFallbackTestLLM) GenerateMultimodalText(_ context.Context, task config.ModelTask, request llm.MultimodalRequest) (string, *llm.CallTrace, error) {
	f.textCalls++
	if !request.TextMode {
		return "", nil, errors.New("visual fallback did not request text mode")
	}
	return "DECISION=SUCCEEDED\nCONFIDENCE=96%\nPRODUCT_SURFACE_VISIBLE=TRUE\nGENERATION_COVERING_SURFACE=FALSE\nSUMMARY=The requested product is complete.\nEVIDENCE_1=Rendered primary surface\nEVIDENCE_2=Two requested controls are visible\nBLOCKING_REASON=NONE", &llm.CallTrace{Provider: config.ModelProviderGLM, Model: "glm-4.5v", Task: task}, nil
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
	bridge, err := startBrowserVisualObserverBridge(t.Context(), client, 10, "A responsive interactive product with a rendered primary surface.", 2, nil)
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

func TestInvokeBrowserVisualProviderReturnsWhenClientIgnoresContext(t *testing.T) {
	client := &blockingBrowserVisualTestLLM{release: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, trace, err := invokeBrowserVisualProvider(ctx, client, llm.MultimodalRequest{User: "public visual fact"}, false)
	close(client.release)
	if err == nil || trace == nil || trace.ErrorClass != "timeout" {
		t.Fatalf("ignored provider cancellation did not become a bounded timeout: trace=%+v err=%v", trace, err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("observer waited for the hung provider after its deadline: %s", elapsed)
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

func TestBrowserVisualObservationNormalizesHighConfidenceSurfaceThreshold(t *testing.T) {
	result, err := normalizeBrowserVisualObservation(browserVisualObservationModelOutput{
		Decision: "in_progress", Confidence: .95, Summary: "The requested product surface is rendered.",
		VisibleEvidence:           []string{"The product identity, data status, and primary controls are visible."},
		BlockingReason:            "Initial data is empty.",
		ProductSurfaceVisible:     true,
		GenerationCoveringSurface: false,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != "succeeded" || result.BlockingReason != "" {
		t.Fatalf("high-confidence product surface was not admitted to deterministic interaction proof: %+v", result)
	}

	result, err = normalizeBrowserVisualObservation(browserVisualObservationModelOutput{
		Decision: "in_progress", Confidence: .95, Summary: "The requested product is still covered by generation.",
		ProductSurfaceVisible: true, GenerationCoveringSurface: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != "in_progress" {
		t.Fatalf("a generation-covered surface was admitted: %+v", result)
	}
}

func TestBrowserVisualObservationFailsCompletedPlaceholderContradiction(t *testing.T) {
	result, err := normalizeBrowserVisualObservation(browserVisualObservationModelOutput{
		Decision: "failed", Confidence: .97, Summary: "The platform says the task completed but still renders its default welcome state.",
		VisibleEvidence: []string{
			"The activity panel visibly says task completed and verified.",
			"The preview still shows a generic Welcome placeholder and no requested product surface.",
		},
		BlockingReason: "Terminal product mismatch",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != "failed" || result.BlockingReason == "" {
		t.Fatalf("completed placeholder contradiction was not terminal: %+v", result)
	}
}

func TestBrowserVisualGateDefersInteractionAndPolishToLayeredProof(t *testing.T) {
	for _, prompt := range []string{browserVisualGateSystemPrompt, browserVisualLineGateSystemPrompt} {
		if !strings.Contains(prompt, "screenshot cannot prove or disprove interactivity") || !strings.Contains(prompt, "Do not score visual polish") || !strings.Contains(prompt, "initial data states") {
			t.Fatalf("visual Gate prompt collapsed later proof layers into preview admission: %q", prompt)
		}
		if !strings.Contains(prompt, "at least two concrete matching identity, status, data, or control elements") {
			t.Fatalf("visual Gate prompt lost its site-neutral core surface threshold: %q", prompt)
		}
		if !strings.Contains(prompt, "same screenshot") || !strings.Contains(prompt, "generic placeholder") {
			t.Fatalf("visual Gate prompt lost terminal placeholder contradiction handling: %q", prompt)
		}
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

func TestBrowserVisualObserverDoesNotRetryHungProvider(t *testing.T) {
	if browserVisualObserverMayFallback(&llm.CallTrace{ErrorClass: "timeout"}) {
		t.Fatal("a timed-out visual call would be retried immediately")
	}
	if !browserVisualObserverMayFallback(&llm.CallTrace{ErrorClass: "json_parse_failed"}) {
		t.Fatal("a fast parse failure should retain its bounded repair fallback")
	}
}

func TestBrowserVisualObserverUsesSingleLineCallWithLargeBudget(t *testing.T) {
	client := &browserVisualFallbackTestLLM{}
	bridge, err := startBrowserVisualObserverBridge(t.Context(), client, 10, "A complete interactive product", 12, nil)
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
	if result.Decision != "succeeded" || result.ProviderCalls != 1 || len(result.VisibleEvidence) != 2 {
		t.Fatalf("single line response was not normalized: %+v", result)
	}
	if !result.ProductSurfaceVisible || result.GenerationCoveringSurface {
		t.Fatalf("bounded fallback lost layered surface facts: %+v", result)
	}
	if client.multimodalCalls != 0 || client.textCalls != 1 {
		t.Fatalf("large-budget line calls=%d/%d", client.multimodalCalls, client.textCalls)
	}
}

func TestBrowserVisualObserverTwoCallBudgetUsesOneParseableLineCall(t *testing.T) {
	client := &browserVisualFallbackTestLLM{}
	bridge, err := startBrowserVisualObserverBridge(t.Context(), client, 10, "A complete interactive product", 2, nil)
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
		t.Fatalf("unexpected bounded line response: status=%d", response.StatusCode)
	}
	if result.Decision != "succeeded" || result.ProviderCalls != 1 || client.multimodalCalls != 0 || client.textCalls != 1 {
		t.Fatalf("two-call budget did not use one parseable provider call: result=%+v calls=%d/%d", result, client.multimodalCalls, client.textCalls)
	}
}

func TestBrowserVisualObservationLimitReservesJSONRepairFallback(t *testing.T) {
	tests := []struct {
		budget int
		want   int
	}{
		{budget: 1, want: 1},
		{budget: 2, want: 2},
		{budget: 5, want: 2},
		{budget: 6, want: 3},
		{budget: 12, want: 6},
	}
	for _, test := range tests {
		if got := browserVisualObservationLimit(test.budget); got != test.want {
			t.Fatalf("budget %d: got %d observations, want %d", test.budget, got, test.want)
		}
	}
}
