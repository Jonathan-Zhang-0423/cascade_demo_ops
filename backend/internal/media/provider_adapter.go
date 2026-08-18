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
		adapter, _ := r.Get(descriptor.Provider)
		preflight := adapter.Preflight(ctx, intent)
		if preflight.Selectable {
			return adapter, preflight, nil
		}
	}
	return nil, GeneratedShotProviderPreflight{}, errors.New("no registered generated shot provider is selectable")
}
