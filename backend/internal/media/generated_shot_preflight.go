package media

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const GeneratedShotPreflightSchemaVersion = "demoops.generated_shot_preflight.v1"

// GeneratedShotProviderAvailability is Server-internal runtime policy. It is
// deliberately separate from a provider's capability profile: a request can
// be capability-compatible while the provider remains disabled.
type GeneratedShotProviderAvailability struct {
	Provider string `json:"provider"`
	Enabled  bool   `json:"enabled"`
	Reason   string `json:"reason,omitempty"`
}

// GeneratedShotProviderPreflight contains no provider client and cannot be
// executed. CompiledRequest is emitted only as a dry-run diagnostic artifact.
type GeneratedShotProviderPreflight struct {
	Provider             string                        `json:"provider"`
	ProfileVersion       string                        `json:"profile_version"`
	CapabilityCompatible bool                          `json:"capability_compatible"`
	Enabled              bool                          `json:"enabled"`
	Selectable           bool                          `json:"selectable"`
	FailureCode          string                        `json:"failure_code,omitempty"`
	FailureField         string                        `json:"failure_field,omitempty"`
	FailureMessage       string                        `json:"failure_message,omitempty"`
	CompiledRequest      *GeneratedShotCompiledRequest `json:"compiled_request,omitempty"`
}

type GeneratedShotPreflightReport struct {
	SchemaVersion string                           `json:"schema_version"`
	IntentID      string                           `json:"intent_id"`
	Executable    bool                             `json:"executable"`
	Providers     []GeneratedShotProviderPreflight `json:"providers"`
	Warnings      []string                         `json:"warnings,omitempty"`
}

// PreflightGeneratedShotIntent evaluates providers independently and never
// invokes a provider. Availability defaults to disabled when it is missing;
// callers must opt a provider in through Server-internal policy.
func PreflightGeneratedShotIntent(intent GeneratedShotIntent, compilers []GeneratedShotProviderCompiler, availability []GeneratedShotProviderAvailability) GeneratedShotPreflightReport {
	report := GeneratedShotPreflightReport{
		SchemaVersion: GeneratedShotPreflightSchemaVersion,
		IntentID:      intent.IntentID,
		Executable:    false,
		Warnings: []string{
			"preflight is diagnostic only and cannot invoke a provider",
			"selectable does not authorize generation or editor timeline insertion",
		},
	}
	availabilityByProvider := make(map[string]GeneratedShotProviderAvailability, len(availability))
	for _, item := range availability {
		if item.Provider == "" {
			continue
		}
		availabilityByProvider[item.Provider] = item
	}

	for _, compiler := range compilers {
		if compiler == nil {
			continue
		}
		profile := compiler.Profile()
		item := GeneratedShotProviderPreflight{
			Provider:       profile.Provider,
			ProfileVersion: profile.ProfileVersion,
		}
		if policy, ok := availabilityByProvider[profile.Provider]; ok {
			item.Enabled = policy.Enabled
			if !policy.Enabled {
				item.FailureCode = "provider_disabled"
				item.FailureField = "provider"
				item.FailureMessage = policy.Reason
			}
		} else {
			item.FailureCode = "provider_disabled"
			item.FailureField = "provider"
			item.FailureMessage = "provider availability was not explicitly enabled by Server policy"
		}

		compiled, err := compiler.Compile(intent)
		if err != nil {
			item.FailureCode, item.FailureField, item.FailureMessage = generatedShotPreflightFailure(err)
		} else {
			item.CapabilityCompatible = true
			item.Selectable = item.Enabled
			compiled.DryRunOnly = true
			item.CompiledRequest = &compiled
		}
		report.Providers = append(report.Providers, item)
	}

	sort.SliceStable(report.Providers, func(i, j int) bool {
		return report.Providers[i].Provider < report.Providers[j].Provider
	})
	return report
}

// ValidateGeneratedShotPreflightReport revalidates a serialized diagnostic
// report. A valid report still has no execution authority.
func ValidateGeneratedShotPreflightReport(report GeneratedShotPreflightReport) error {
	if report.SchemaVersion != GeneratedShotPreflightSchemaVersion || strings.TrimSpace(report.IntentID) == "" {
		return &GeneratedShotProviderValidationError{Provider: "preflight", Field: "schema_version", Code: "generated_preflight_identity_invalid", Message: "preflight schema and intent_id are required"}
	}
	if report.Executable {
		return &GeneratedShotProviderValidationError{Provider: "preflight", Field: "executable", Code: "generated_preflight_execution_forbidden", Message: "preflight report must remain non-executable"}
	}
	if len(report.Providers) == 0 {
		return &GeneratedShotProviderValidationError{Provider: "preflight", Field: "providers", Code: "generated_preflight_providers_missing", Message: "at least one provider diagnostic is required"}
	}
	seen := map[string]struct{}{}
	for index, provider := range report.Providers {
		field := fmt.Sprintf("providers[%d]", index)
		if !isSupportedGeneratedShotProvider(provider.Provider) {
			return &GeneratedShotProviderValidationError{Provider: provider.Provider, Field: field + ".provider", Code: "generated_preflight_provider_unsupported", Message: "provider is unsupported"}
		}
		if _, exists := seen[provider.Provider]; exists {
			return &GeneratedShotProviderValidationError{Provider: provider.Provider, Field: field + ".provider", Code: "generated_preflight_provider_duplicate", Message: "provider diagnostics must be unique"}
		}
		seen[provider.Provider] = struct{}{}
		if strings.TrimSpace(provider.ProfileVersion) == "" {
			return &GeneratedShotProviderValidationError{Provider: provider.Provider, Field: field + ".profile_version", Code: "generated_preflight_profile_missing", Message: "profile_version is required"}
		}
		if provider.Selectable && (!provider.CapabilityCompatible || !provider.Enabled) {
			return &GeneratedShotProviderValidationError{Provider: provider.Provider, Field: field + ".selectable", Code: "generated_preflight_selectable_invalid", Message: "selectable requires compatible and enabled"}
		}
		if provider.CapabilityCompatible {
			if provider.CompiledRequest == nil || !provider.CompiledRequest.DryRunOnly {
				return &GeneratedShotProviderValidationError{Provider: provider.Provider, Field: field + ".compiled_request", Code: "generated_preflight_dry_run_missing", Message: "compatible provider requires a dry-run compiled request"}
			}
			compiled := provider.CompiledRequest
			if compiled.Provider != provider.Provider || compiled.ProfileVersion != provider.ProfileVersion || compiled.IntentID != report.IntentID {
				return &GeneratedShotProviderValidationError{Provider: provider.Provider, Field: field + ".compiled_request", Code: "generated_preflight_compiled_binding_mismatch", Message: "compiled request must bind provider, profile, and intent"}
			}
		} else {
			if provider.CompiledRequest != nil || strings.TrimSpace(provider.FailureCode) == "" {
				return &GeneratedShotProviderValidationError{Provider: provider.Provider, Field: field, Code: "generated_preflight_failure_invalid", Message: "incompatible provider requires a failure and no compiled request"}
			}
		}
	}
	return nil
}

func generatedShotPreflightFailure(err error) (code string, field string, message string) {
	var common *GeneratedShotIntentValidationError
	if errors.As(err, &common) {
		return common.Code, common.Field, common.Message
	}
	var provider *GeneratedShotProviderValidationError
	if errors.As(err, &provider) {
		return provider.Code, provider.Field, provider.Message
	}
	return "generated_shot_preflight_failed", "request", err.Error()
}
