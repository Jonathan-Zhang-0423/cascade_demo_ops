package media

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const GeneratedShotProviderExecutionSchemaVersion = "demoops.generated_shot_provider_execution.v1"

type GeneratedShotProviderDescriptor struct {
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	ProfileVersion string `json:"profile_version"`
	Enabled        bool   `json:"enabled"`
	Reason         string `json:"reason,omitempty"`
}

type GeneratedShotProviderExecutionRequest struct {
	Intent               GeneratedShotIntent `json:"intent"`
	GenerationAuthorized bool                `json:"generation_authorized"`
	AuthorizationRef     string              `json:"authorization_ref"`
	IdempotencyKey       string              `json:"idempotency_key"`
	AdmissionScope       string              `json:"admission_scope"`
	OutputDir            string              `json:"output_dir"`
	Timeout              time.Duration       `json:"timeout"`
	ResumeProviderTaskID string              `json:"resume_provider_task_id,omitempty"`
}

type GeneratedShotProviderExecutionResult struct {
	SchemaVersion    string                         `json:"schema_version"`
	Provider         string                         `json:"provider"`
	Model            string                         `json:"model"`
	IntentID         string                         `json:"intent_id"`
	Status           string                         `json:"status"`
	ProviderTaskID   string                         `json:"provider_task_id,omitempty"`
	Candidate        *GeneratedShotCandidate        `json:"candidate,omitempty"`
	StructuralReview *GeneratedShotStructuralReview `json:"structural_review,omitempty"`
	FailurePolicy    string                         `json:"failure_policy"`
	ErrorClass       string                         `json:"error_class,omitempty"`
	ErrorMessage     string                         `json:"error_message,omitempty"`
}

type GeneratedShotProviderAdapter interface {
	Descriptor() GeneratedShotProviderDescriptor
	Profile() GeneratedShotCapabilityProfile
	Preflight(context.Context, GeneratedShotIntent) GeneratedShotProviderPreflight
	Execute(context.Context, GeneratedShotProviderExecutionRequest) (GeneratedShotProviderExecutionResult, error)
	Cancel(context.Context, string) error
}

type GeneratedShotProviderRegistry struct {
	mu       sync.RWMutex
	adapters map[string]GeneratedShotProviderAdapter
}

type GeneratedShotProviderFallbackAttempt struct {
	Provider       string `json:"provider"`
	TaskID         string `json:"task_id,omitempty"`
	FallbackReason string `json:"fallback_reason,omitempty"`
	Status         string `json:"status,omitempty"`
	ErrorClass     string `json:"error_class,omitempty"`
}

type GeneratedShotProviderPrepare func(context.Context, GeneratedShotProviderAdapter, GeneratedShotIntent) (GeneratedShotIntent, error)

// OrderedCandidates returns selectable adapters in deterministic preference
// order. It is intentionally separate from Select so callers that need
// compatibility fallback can attempt the next provider only when no provider
// task was created.
func (r *GeneratedShotProviderRegistry) OrderedCandidates(ctx context.Context, intent GeneratedShotIntent, preferredProvider string) ([]GeneratedShotProviderAdapter, []GeneratedShotProviderPreflight) {
	if err := ValidateGeneratedShotIntent(intent); err != nil {
		return nil, []GeneratedShotProviderPreflight{{FailureMessage: err.Error()}}
	}
	descriptors := r.Descriptors()
	ordered := make([]GeneratedShotProviderDescriptor, 0, len(descriptors))
	if preferred := strings.TrimSpace(preferredProvider); preferred != "" {
		for _, descriptor := range descriptors {
			if descriptor.Provider == preferred {
				ordered = append(ordered, descriptor)
			}
		}
	}
	for _, descriptor := range descriptors {
		if descriptor.Provider != strings.TrimSpace(preferredProvider) {
			ordered = append(ordered, descriptor)
		}
	}
	adapters := make([]GeneratedShotProviderAdapter, 0, len(ordered))
	preflights := make([]GeneratedShotProviderPreflight, 0, len(ordered))
	for _, descriptor := range ordered {
		if len(intent.AllowedProviders) > 0 && !containsGeneratedShotValue(intent.AllowedProviders, descriptor.Provider) {
			continue
		}
		adapter, ok := r.Get(descriptor.Provider)
		if !ok {
			continue
		}
		preflight := adapter.Preflight(ctx, intent)
		preflights = append(preflights, preflight)
		if preflight.Selectable {
			adapters = append(adapters, adapter)
		}
	}
	return adapters, preflights
}

// ExecuteWithFallback tries compatible providers in deterministic order. A
// provider task that has already been created is never replaced automatically:
// callers must resume that task by ID to avoid duplicate charges.
func (r *GeneratedShotProviderRegistry) ExecuteWithFallback(ctx context.Context, request GeneratedShotProviderExecutionRequest, preferredProvider string) (GeneratedShotProviderExecutionResult, []GeneratedShotProviderFallbackAttempt) {
	result, executions := r.ExecuteWithFallbackPrepared(ctx, request, preferredProvider, nil)
	attempts := make([]GeneratedShotProviderFallbackAttempt, 0, len(executions))
	for index, execution := range executions {
		attempt := GeneratedShotProviderFallbackAttempt{Provider: execution.Provider, TaskID: execution.ProviderTaskID, Status: execution.Status, ErrorClass: execution.ErrorClass}
		if execution.ProviderTaskID != "" || request.ResumeProviderTaskID != "" {
			attempt.FallbackReason = "task_created_or_resumed; require_task_recovery"
		} else if index+1 < len(executions) {
			attempt.FallbackReason = "provider_failed_before_task_creation"
		}
		attempts = append(attempts, attempt)
	}
	return result, attempts
}

// ExecuteWithFallbackPrepared is the single ProviderPool execution path. The
// optional prepare hook is where a caller may publish references or normalize
// an intent; fallback still remains owned by this registry and never retries
// after a task ID has been created.
func (r *GeneratedShotProviderRegistry) ExecuteWithFallbackPrepared(ctx context.Context, request GeneratedShotProviderExecutionRequest, preferredProvider string, prepare GeneratedShotProviderPrepare) (GeneratedShotProviderExecutionResult, []GeneratedShotProviderExecutionResult) {
	adapters, preflights := r.OrderedCandidates(ctx, request.Intent, preferredProvider)
	executions := make([]GeneratedShotProviderExecutionResult, 0, len(preflights))
	if len(adapters) == 0 {
		message := "no compatible provider passed preflight"
		if len(preflights) > 0 && strings.TrimSpace(preflights[0].FailureMessage) != "" {
			message = preflights[0].FailureMessage
		}
		result := GeneratedShotProviderExecutionResult{SchemaVersion: GeneratedShotProviderExecutionSchemaVersion, IntentID: request.Intent.IntentID, Status: GeneratedShotFailureContinue, FailurePolicy: GeneratedShotFailureContinue, ErrorClass: "no_compatible_provider", ErrorMessage: message}
		return result, []GeneratedShotProviderExecutionResult{result}
	}
	for _, adapter := range adapters {
		providerIntent := request.Intent
		if prepare != nil {
			prepared, err := prepare(ctx, adapter, request.Intent)
			if err != nil {
				result := GeneratedShotProviderExecutionResult{SchemaVersion: GeneratedShotProviderExecutionSchemaVersion, Provider: adapter.Descriptor().Provider, Model: adapter.Descriptor().Model, IntentID: request.Intent.IntentID, Status: GeneratedShotFailureContinue, FailurePolicy: GeneratedShotFailureContinue, ErrorClass: "provider_prepare_failed", ErrorMessage: err.Error()}
				executions = append(executions, result)
				continue
			}
			providerIntent = prepared
		}
		providerRequest := request
		providerRequest.Intent = providerIntent
		result, err := adapter.Execute(ctx, providerRequest)
		if err != nil && strings.TrimSpace(result.ErrorMessage) == "" {
			result.ErrorMessage = err.Error()
		}
		if result.SchemaVersion == "" {
			result.SchemaVersion = GeneratedShotProviderExecutionSchemaVersion
		}
		if result.Provider == "" {
			result.Provider = adapter.Descriptor().Provider
		}
		if result.Model == "" {
			result.Model = adapter.Descriptor().Model
		}
		if result.IntentID == "" {
			result.IntentID = request.Intent.IntentID
		}
		if result.FailurePolicy == "" {
			result.FailurePolicy = GeneratedShotFailureContinue
		}
		executions = append(executions, result)
		if result.Candidate != nil || result.ProviderTaskID != "" || request.ResumeProviderTaskID != "" {
			return result, executions
		}
	}
	if len(executions) == 0 {
		result := GeneratedShotProviderExecutionResult{SchemaVersion: GeneratedShotProviderExecutionSchemaVersion, IntentID: request.Intent.IntentID, Status: GeneratedShotFailureContinue, FailurePolicy: GeneratedShotFailureContinue, ErrorClass: "provider_fallback_exhausted"}
		return result, []GeneratedShotProviderExecutionResult{result}
	}
	return executions[len(executions)-1], executions
}

func NewGeneratedShotProviderRegistry() *GeneratedShotProviderRegistry {
	return &GeneratedShotProviderRegistry{adapters: map[string]GeneratedShotProviderAdapter{}}
}

func (r *GeneratedShotProviderRegistry) Register(adapter GeneratedShotProviderAdapter) error {
	if adapter == nil {
		return errors.New("generated shot provider adapter is required")
	}
	descriptor := adapter.Descriptor()
	provider := strings.TrimSpace(descriptor.Provider)
	profile := adapter.Profile()
	if provider == "" || descriptor.Model == "" || descriptor.ProfileVersion == "" || profile.Provider != provider || profile.ProfileVersion != descriptor.ProfileVersion {
		return errors.New("generated shot provider descriptor and profile are inconsistent")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[provider]; exists {
		return fmt.Errorf("generated shot provider %s is already registered", provider)
	}
	r.adapters[provider] = adapter
	return nil
}

func (r *GeneratedShotProviderRegistry) Get(provider string) (GeneratedShotProviderAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[strings.TrimSpace(provider)]
	return adapter, ok
}

func (r *GeneratedShotProviderRegistry) Descriptors() []GeneratedShotProviderDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]GeneratedShotProviderDescriptor, 0, len(r.adapters))
	for _, adapter := range r.adapters {
		result = append(result, adapter.Descriptor())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Provider < result[j].Provider })
	return result
}

// Select performs capability and runtime-availability checks only. It never
// authorizes generation and never invokes Execute.
func (r *GeneratedShotProviderRegistry) Select(ctx context.Context, intent GeneratedShotIntent, preferredProvider string) (GeneratedShotProviderAdapter, GeneratedShotProviderPreflight, error) {
	if err := ValidateGeneratedShotIntent(intent); err != nil {
		return nil, GeneratedShotProviderPreflight{}, err
	}
	if preferred := strings.TrimSpace(preferredProvider); preferred != "" {
		if len(intent.AllowedProviders) > 0 && !containsGeneratedShotValue(intent.AllowedProviders, preferred) {
			return nil, GeneratedShotProviderPreflight{}, fmt.Errorf("preferred generated shot provider %s is outside the intent allowlist", preferred)
		}
		adapter, ok := r.Get(preferred)
		if !ok {
			return nil, GeneratedShotProviderPreflight{}, fmt.Errorf("preferred generated shot provider %s is not registered", preferred)
		}
		preflight := adapter.Preflight(ctx, intent)
		if !preflight.Selectable {
			return nil, preflight, fmt.Errorf("preferred generated shot provider %s is not selectable: %s", preferred, preflight.FailureMessage)
		}
		return adapter, preflight, nil
	}
	for _, descriptor := range r.Descriptors() {
		if len(intent.AllowedProviders) > 0 && !containsGeneratedShotValue(intent.AllowedProviders, descriptor.Provider) {
			continue
		}
		adapter, _ := r.Get(descriptor.Provider)
		preflight := adapter.Preflight(ctx, intent)
		if preflight.Selectable {
			return adapter, preflight, nil
		}
	}
	return nil, GeneratedShotProviderPreflight{}, errors.New("no registered generated shot provider is selectable")
}
