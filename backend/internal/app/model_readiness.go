package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
)

const modelReadinessSchemaVersion = "cascade.model_readiness.v1"

// ModelReadinessFinding is a redacted, actionable model preflight finding.
// It intentionally contains no API key, prompt, response body, or local path.
type ModelReadinessFinding struct {
	Code       string               `json:"code"`
	Task       config.ModelTask     `json:"task,omitempty"`
	Provider   config.ModelProvider `json:"provider,omitempty"`
	Model      string               `json:"model,omitempty"`
	ErrorClass string               `json:"error_class,omitempty"`
	Message    string               `json:"message"`
}

// ModelReadinessView is a server-owned preflight summary for the model
// routes required by the App planning and Server media pipeline.
type ModelReadinessView struct {
	SchemaVersion string                  `json:"schema_version"`
	Ready         bool                    `json:"ready"`
	Mode          config.LLMMode          `json:"mode"`
	CheckedAt     time.Time               `json:"checked_at"`
	RequiredTasks []config.ModelTask      `json:"required_tasks"`
	Diagnostics   []llm.DiagnosticResult  `json:"diagnostics"`
	Findings      []ModelReadinessFinding `json:"findings,omitempty"`
}

var requiredModelTasks = []config.ModelTask{
	config.ModelTaskPlanning,
	config.ModelTaskCodeReading,
	config.ModelTaskMultimodalUnderstanding,
	config.ModelTaskBrowserVisualObservation,
	config.ModelTaskVideoOperation,
}

// DiagnoseModelReadiness performs a real, redacted probe for every required
// route. It is intentionally separate from package execution: an existing
// unchanged App package can still be tested by Server without re-calling LLMs.
func (s *Service) DiagnoseModelReadiness(ctx context.Context) ModelReadinessView {
	now := time.Now().UTC()
	view := ModelReadinessView{
		SchemaVersion: modelReadinessSchemaVersion,
		Ready:         true,
		Mode:          config.LLMModeAuto,
		CheckedAt:     now,
		RequiredTasks: append([]config.ModelTask{}, requiredModelTasks...),
		Diagnostics:   []llm.DiagnosticResult{},
		Findings:      []ModelReadinessFinding{},
	}
	if s == nil {
		view.Ready = false
		view.Findings = append(view.Findings, ModelReadinessFinding{Code: "service_missing", Message: "Server model router is unavailable"})
		return view
	}
	if s.llm == nil {
		view.Ready = false
		view.Findings = append(view.Findings, ModelReadinessFinding{Code: "router_missing", Message: "Server model router is unavailable"})
		return view
	}
	runtime := s.RuntimeConfig()
	view.Mode = runtime.LLMMode
	for _, task := range requiredModelTasks {
		view.Diagnostics = append(view.Diagnostics, s.DiagnosePlanningOrTask(ctx, task))
	}
	sort.Slice(view.Diagnostics, func(i, j int) bool { return view.Diagnostics[i].Task < view.Diagnostics[j].Task })
	for _, diagnostic := range view.Diagnostics {
		if diagnostic.Configured && diagnostic.OK {
			continue
		}
		view.Ready = false
		code := diagnostic.ErrorClass
		if !diagnostic.Configured {
			code = "model_not_configured"
		}
		if strings.TrimSpace(code) == "" {
			code = "model_probe_failed"
		}
		message := "Model preflight failed; inspect the provider configuration and network diagnostics"
		if code == "model_not_configured" {
			message = "Required model route is not configured"
		}
		view.Findings = append(view.Findings, ModelReadinessFinding{
			Code: code, Task: diagnostic.Task, Provider: diagnostic.Provider,
			Model: diagnostic.Model, ErrorClass: diagnostic.ErrorClass, Message: message,
		})
	}
	return view
}

// DiagnosePlanningOrTask keeps the public Service surface small while making
// the readiness endpoint testable for every task without exposing router state.
func (s *Service) DiagnosePlanningOrTask(ctx context.Context, task config.ModelTask) llm.DiagnosticResult {
	return s.llm.DiagnoseTask(ctx, task)
}
