package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
)

const (
	browserVisualObservationSchemaVersion = "demoops.browser_visual_observation.v1"
	browserVisualObservationMaxBodyBytes  = 16 << 20
)

type browserVisualObservationRequest struct {
	SchemaVersion     string `json:"schema_version"`
	ObservationKind   string `json:"observation_kind,omitempty"`
	RunID             string `json:"run_id,omitempty"`
	StageID           string `json:"stage_id"`
	NodeID            string `json:"node_id"`
	StageOrder        int    `json:"stage_order"`
	Sequence          int    `json:"sequence"`
	ElapsedMS         int64  `json:"elapsed_ms"`
	SemanticGoal      string `json:"semantic_goal"`
	ExpectedState     string `json:"expected_state"`
	ExpectedBoolean   *bool  `json:"expected_boolean,omitempty"`
	CurrentURL        string `json:"current_url,omitempty"`
	PageTitle         string `json:"page_title,omitempty"`
	ScreenshotDataURI string `json:"screenshot_data_uri"`
}

type browserVisualObservationResponse struct {
	SchemaVersion   string         `json:"schema_version"`
	Decision        string         `json:"decision"`
	Confidence      float64        `json:"confidence"`
	Summary         string         `json:"summary"`
	VisibleEvidence []string       `json:"visible_evidence,omitempty"`
	BlockingReason  string         `json:"blocking_reason,omitempty"`
	ModelTrace      map[string]any `json:"model_trace,omitempty"`
	ObservedAt      time.Time      `json:"observed_at"`
}

type browserVisualObservationModelOutput struct {
	Decision        string   `json:"decision"`
	Confidence      float64  `json:"confidence"`
	Summary         string   `json:"summary"`
	VisibleEvidence []string `json:"visible_evidence"`
	BlockingReason  string   `json:"blocking_reason"`
}

func (o *browserVisualObservationModelOutput) UnmarshalJSON(data []byte) error {
	type wireOutput struct {
		Decision        string          `json:"decision"`
		Confidence      json.RawMessage `json:"confidence"`
		Summary         string          `json:"summary"`
		VisibleEvidence json.RawMessage `json:"visible_evidence"`
		BlockingReason  string          `json:"blocking_reason"`
		Answer          json.RawMessage `json:"answer"`
	}
	var wire wireOutput
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if len(wire.Answer) > 0 && strings.TrimSpace(wire.Decision) == "" {
		var nested string
		if err := json.Unmarshal(wire.Answer, &nested); err == nil {
			return json.Unmarshal([]byte(strings.TrimSpace(nested)), o)
		}
		return json.Unmarshal(wire.Answer, o)
	}
	confidence, err := decodeBrowserVisualConfidence(wire.Confidence)
	if err != nil {
		return err
	}
	visibleEvidence, err := decodeBrowserVisualEvidence(wire.VisibleEvidence)
	if err != nil {
		return err
	}
	*o = browserVisualObservationModelOutput{
		Decision: wire.Decision, Confidence: confidence, Summary: wire.Summary,
		VisibleEvidence: visibleEvidence, BlockingReason: wire.BlockingReason,
	}
	return nil
}

func decodeBrowserVisualConfidence(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	normalized := strings.ToLower(strings.TrimSpace(text))
	switch normalized {
	case "high", "very high", "very_high":
		return 0.95, nil
	case "medium", "moderate":
		return 0.70, nil
	case "low":
		return 0.40, nil
	}
	if strings.HasSuffix(normalized, "%") {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(normalized, "%")), 64)
		return parsed / 100, err
	}
	return strconv.ParseFloat(normalized, 64)
}

func decodeBrowserVisualEvidence(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return values, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	return []string{value}, nil
}

type browserVisualObserverBridge struct {
	URL    string
	Token  string
	server *http.Server
	ln     net.Listener
}

func (b *browserVisualObserverBridge) Close() {
	if b == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = b.server.Shutdown(ctx)
	_ = b.ln.Close()
}

func startBrowserVisualObserverBridge(
	parent context.Context,
	client llm.Client,
	stageCount int,
	progress func(stage string, message string, progress int),
) (*browserVisualObserverBridge, error) {
	if client == nil {
		return nil, errors.New("browser visual observer model is not configured")
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	server := &http.Server{ReadHeaderTimeout: 3 * time.Second}
	bridge := &browserVisualObserverBridge{URL: "http://" + listener.Addr().String() + "/v1/observe", Token: token, server: server, ln: listener}
	mux.HandleFunc("POST /v1/observe", func(w http.ResponseWriter, r *http.Request) {
		if !constantTimeBearerMatch(r.Header.Get("Authorization"), token) {
			writeBrowserVisualObserverError(w, http.StatusUnauthorized, "observer_auth_invalid")
			return
		}
		var request browserVisualObservationRequest
		decoder := json.NewDecoder(io.LimitReader(r.Body, browserVisualObservationMaxBodyBytes+1))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeBrowserVisualObserverError(w, http.StatusBadRequest, "observer_request_invalid")
			return
		}
		if err := validateBrowserVisualObservationRequest(request); err != nil {
			writeBrowserVisualObserverError(w, http.StatusBadRequest, err.Error())
			return
		}
		if progress != nil {
			percent := 40
			if stageCount > 0 {
				percent += request.StageOrder * 44 / stageCount
			}
			if percent > 84 {
				percent = 84
			}
			progress("browser_visual_observation", fmt.Sprintf("正在理解第 %d 阶段的第 %d 张轮询截图。", request.StageOrder, request.Sequence), percent)
		}
		var output browserVisualObservationModelOutput
		systemPrompt := "You are a browser-runtime visual observer. Judge only what is visibly supported by the supplied screenshot. Never infer from a hostname, remembered product, DOM selector, route, prior conversation, or elapsed time. Return one top-level JSON object with exactly decision, confidence, summary, visible_evidence, and blocking_reason; do not wrap it in answer or data. decision must be one of in_progress, succeeded, failed, unknown. Use succeeded only when the requested expected state is visibly proven. Use failed only when the screenshot visibly contains an explicit terminal error, failure banner, denial, cancellation, or unrecoverable exception. If success is not visible and no explicit terminal failure is visible, return in_progress, including for a blank/initial page, unchanged page, spinner, missing result, or one/minutes of elapsed polling. Elapsed polling time is context, never a deadline. A project card, blank page, spinner disappearance, or unrelated page is not proof of success. Keep evidence short and quote only visible UI text."
		userPrompt := fmt.Sprintf("Semantic goal: %s\nExpected visible state: %s\nElapsed polling time (context only, not a timeout): %d ms\nScreenshot sequence: %d\nCurrent page title (untrusted hint): %s\nClassify this screenshot and list the visible evidence. Do not claim success unless the screenshot directly proves the expected state. Do not claim failure merely because the result is not complete yet.", limitObserverText(request.SemanticGoal, 1200), limitObserverText(request.ExpectedState, 1200), request.ElapsedMS, request.Sequence, limitObserverText(request.PageTitle, 300))
		if request.ObservationKind == "toggle_state" {
			expected := request.ExpectedBoolean != nil && *request.ExpectedBoolean
			systemPrompt = "You are a browser-runtime visual observer examining a tightly cropped, evidence-bound toggle control. Judge only whether this exact control is visibly on/checked/selected or off/unchecked/unselected. Never infer from hostname, product memory, DOM, selector, or prior conversation. Return one top-level JSON object with exactly decision, confidence, summary, visible_evidence, and blocking_reason. Return succeeded when the visible control state equals the requested boolean, failed when the visible control state is clearly the opposite, and unknown when it cannot be determined. Do not use in_progress. A colored filled box with a check mark is checked; an empty outlined box is unchecked. Keep evidence short and describe visible shape, fill, and mark without inventing text."
			userPrompt = fmt.Sprintf("Expected checked/on/selected state: %t\nSemantic goal: %s\nThis screenshot contains only the exact approved control. Determine its current visual state and compare it to the expected boolean.", expected, limitObserverText(request.SemanticGoal, 1200))
		}
		modelRequest := llm.MultimodalRequest{
			System:      systemPrompt,
			User:        userPrompt,
			Images:      []llm.ImageInput{{MimeType: "image/png", DataURI: request.ScreenshotDataURI, Label: "redacted_browser_viewport"}},
			SchemaName:  "browser_visual_observation",
			MaxTokens:   700,
			Temperature: 0,
		}
		var trace *llm.CallTrace
		var err error
		for attempt := 1; attempt <= 2; attempt++ {
			callCtx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
			output = browserVisualObservationModelOutput{}
			trace, err = client.GenerateMultimodal(callCtx, config.ModelTaskBrowserVisualObservation, modelRequest, &output)
			cancel()
			if err == nil || attempt == 2 || r.Context().Err() != nil {
				break
			}
			select {
			case <-r.Context().Done():
				err = r.Context().Err()
			case <-time.After(1500 * time.Millisecond):
			}
		}
		if err != nil {
			errorClass := "provider_error"
			if trace != nil && strings.TrimSpace(trace.ErrorClass) != "" {
				errorClass = strings.TrimSpace(trace.ErrorClass)
			} else if trace != nil && strings.TrimSpace(trace.FallbackReason) != "" {
				errorClass = strings.TrimSpace(trace.FallbackReason)
			}
			log.Printf("browser_visual_observer_error kind=%s provider=%s model=%s error_class=%s", firstNonEmptyObserver(request.ObservationKind, "task_terminal"), safeObserverTraceField(trace, "provider"), safeObserverTraceField(trace, "model"), limitObserverText(errorClass, 80))
			writeBrowserVisualObserverError(w, http.StatusServiceUnavailable, "observer_model_unavailable")
			return
		}
		response, err := normalizeBrowserVisualObservationForKind(output, trace, request.ObservationKind)
		if err != nil {
			writeBrowserVisualObserverError(w, http.StatusBadGateway, "observer_model_output_invalid")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(response)
	})
	server.Handler = mux
	go func() {
		<-parent.Done()
		bridge.Close()
	}()
	go func() { _ = server.Serve(listener) }()
	return bridge, nil
}

func firstNonEmptyObserver(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func safeObserverTraceField(trace *llm.CallTrace, field string) string {
	if trace == nil {
		return "unknown"
	}
	switch field {
	case "provider":
		return limitObserverText(string(trace.Provider), 80)
	case "model":
		return limitObserverText(trace.Model, 120)
	default:
		return "unknown"
	}
}

func validateBrowserVisualObservationRequest(request browserVisualObservationRequest) error {
	if request.SchemaVersion != browserVisualObservationSchemaVersion || strings.TrimSpace(request.StageID) == "" || strings.TrimSpace(request.NodeID) == "" || request.StageOrder < 1 || request.Sequence < 1 {
		return errors.New("observer_identity_invalid")
	}
	if strings.TrimSpace(request.SemanticGoal) == "" || strings.TrimSpace(request.ExpectedState) == "" {
		return errors.New("observer_goal_invalid")
	}
	if request.ObservationKind != "" && request.ObservationKind != "task_terminal" && request.ObservationKind != "toggle_state" {
		return errors.New("observer_kind_invalid")
	}
	if request.ObservationKind == "toggle_state" && request.ExpectedBoolean == nil {
		return errors.New("observer_expected_boolean_missing")
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(request.ScreenshotDataURI, prefix) || len(request.ScreenshotDataURI) > browserVisualObservationMaxBodyBytes {
		return errors.New("observer_screenshot_invalid")
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(request.ScreenshotDataURI, prefix)); err != nil {
		return errors.New("observer_screenshot_invalid")
	}
	return nil
}

func normalizeBrowserVisualObservation(output browserVisualObservationModelOutput, trace *llm.CallTrace) (browserVisualObservationResponse, error) {
	return normalizeBrowserVisualObservationForKind(output, trace, "task_terminal")
}

func normalizeBrowserVisualObservationForKind(output browserVisualObservationModelOutput, trace *llm.CallTrace, observationKind string) (browserVisualObservationResponse, error) {
	decision := strings.ToLower(strings.TrimSpace(output.Decision))
	if observationKind != "toggle_state" && decision == "failed" && !browserVisualEvidenceShowsTerminalFailure(output.VisibleEvidence) {
		// A model may treat the current poll interval as the overall task
		// deadline.  The deterministic runtime owns that deadline; visual
		// evidence may fail early only when it quotes an explicit terminal error.
		decision = "in_progress"
		output.BlockingReason = ""
	}
	switch decision {
	case "in_progress", "succeeded", "failed", "unknown":
	default:
		return browserVisualObservationResponse{}, errors.New("unsupported browser visual decision")
	}
	if output.Confidence < 0 || output.Confidence > 1 || strings.TrimSpace(output.Summary) == "" {
		return browserVisualObservationResponse{}, errors.New("invalid browser visual confidence or summary")
	}
	evidence := make([]string, 0, len(output.VisibleEvidence))
	for _, item := range output.VisibleEvidence {
		if value := limitObserverText(item, 300); value != "" {
			evidence = append(evidence, value)
		}
		if len(evidence) == 8 {
			break
		}
	}
	return browserVisualObservationResponse{
		SchemaVersion: browserVisualObservationSchemaVersion,
		Decision:      decision, Confidence: output.Confidence,
		Summary: limitObserverText(output.Summary, 800), VisibleEvidence: evidence,
		BlockingReason: limitObserverText(output.BlockingReason, 500),
		ModelTrace:     trace.Metadata(), ObservedAt: time.Now().UTC(),
	}, nil
}

func browserVisualEvidenceShowsTerminalFailure(evidence []string) bool {
	for _, item := range evidence {
		if containsAnyFold(item,
			"build failed", "task failed", "generation failed", "error", "exception", "denied", "forbidden", "unauthorized", "canceled", "cancelled",
			"构建失败", "任务失败", "生成失败", "错误", "异常", "拒绝", "无权限", "已取消", "已终止",
		) {
			return true
		}
	}
	return false
}

func containsAnyFold(value string, candidates ...string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range candidates {
		if strings.Contains(value, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func constantTimeBearerMatch(header, token string) bool {
	provided := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(header), "Bearer "))
	if len(provided) != len(token) || provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

func limitObserverText(value string, limit int) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

func writeBrowserVisualObserverError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
