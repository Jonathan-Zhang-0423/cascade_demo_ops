package media

import (
	"context"
	"errors"
	"strings"
	"time"
)

type MiniMaxH3TaskCanceller interface {
	CancelOrDeleteContentGenerationTask(context.Context, string) (MiniMaxH3TaskMutationResult, error)
}

type MiniMaxH3ProviderAdapterOptions struct {
	Enabled                bool
	DisabledReason         string
	Client                 MiniMaxH3HarnessClient
	Canceller              MiniMaxH3TaskCanceller
	Submitter              MiniMaxH3HarnessSubmitter
	Downloader             MiniMaxH3OutputDownloader
	Normalizer             MiniMaxH3MediaNormalizer
	Resolution             string
	UseContextIR           bool
	ContextIRPollAttempts  int
	ContextIRPollInterval  time.Duration
	GenerationPollAttempts int
	GenerationPollInterval time.Duration
	Now                    func() time.Time
}

type MiniMaxH3ProviderAdapter struct {
	options MiniMaxH3ProviderAdapterOptions
}

func NewMiniMaxH3ProviderAdapter(options MiniMaxH3ProviderAdapterOptions) (*MiniMaxH3ProviderAdapter, error) {
	if options.Enabled && (options.Client == nil || options.Submitter == nil) {
		return nil, errors.New("enabled MiniMax-H3 provider adapter requires a client and governed submitter")
	}
	if options.Enabled && (options.Downloader == nil || options.Normalizer == nil) {
		return nil, errors.New("enabled MiniMax-H3 provider adapter requires downloader and normalizer")
	}
	return &MiniMaxH3ProviderAdapter{options: options}, nil
}

func (a *MiniMaxH3ProviderAdapter) Descriptor() GeneratedShotProviderDescriptor {
	reason := strings.TrimSpace(a.options.DisabledReason)
	if !a.options.Enabled && reason == "" {
		reason = "MiniMax-H3 provider adapter is disabled by Server policy"
	}
	return GeneratedShotProviderDescriptor{
		Provider: GeneratedShotProviderMiniMaxH3, Model: MiniMaxH3Model,
		ProfileVersion: (MiniMaxH3GeneratedShotCompiler{}).Profile().ProfileVersion,
		Enabled:        a.options.Enabled, Reason: reason,
	}
}

func (a *MiniMaxH3ProviderAdapter) Profile() GeneratedShotCapabilityProfile {
	return (MiniMaxH3GeneratedShotCompiler{}).Profile()
}

func (a *MiniMaxH3ProviderAdapter) Preflight(_ context.Context, intent GeneratedShotIntent) GeneratedShotProviderPreflight {
	report := PreflightGeneratedShotIntent(intent, []GeneratedShotProviderCompiler{MiniMaxH3GeneratedShotCompiler{}}, []GeneratedShotProviderAvailability{{
		Provider: GeneratedShotProviderMiniMaxH3, Enabled: a.options.Enabled, Reason: a.Descriptor().Reason,
	}})
	if len(report.Providers) == 0 {
		return GeneratedShotProviderPreflight{Provider: GeneratedShotProviderMiniMaxH3, FailureCode: "provider_preflight_missing", FailureMessage: "MiniMax-H3 preflight produced no provider result"}
	}
	return report.Providers[0]
}

func (a *MiniMaxH3ProviderAdapter) Execute(ctx context.Context, request GeneratedShotProviderExecutionRequest) (GeneratedShotProviderExecutionResult, error) {
	result := GeneratedShotProviderExecutionResult{
		SchemaVersion: GeneratedShotProviderExecutionSchemaVersion, Provider: GeneratedShotProviderMiniMaxH3,
		Model: MiniMaxH3Model, IntentID: request.Intent.IntentID, Status: GeneratedShotFailureContinue,
		FailurePolicy: GeneratedShotFailureContinue,
	}
	fail := func(class string, err error) (GeneratedShotProviderExecutionResult, error) {
		result.ErrorClass = class
		if err != nil {
			result.ErrorMessage = err.Error()
		}
		return result, err
	}
	if !request.GenerationAuthorized || strings.TrimSpace(request.AuthorizationRef) == "" {
		return fail("generation_authorization_required", errors.New("explicit persisted generation authorization is required before provider execution"))
	}
	if !a.options.Enabled {
		return fail("provider_disabled", errors.New(a.Descriptor().Reason))
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.AdmissionScope) == "" || strings.TrimSpace(request.OutputDir) == "" {
		return fail("provider_execution_request_invalid", errors.New("idempotency_key, admission_scope, and output_dir are required"))
	}
	preflight := a.Preflight(ctx, request.Intent)
	if !preflight.Selectable {
		return fail(firstNonEmpty(preflight.FailureCode, "provider_preflight_failed"), errors.New(preflight.FailureMessage))
	}
	harness, err := RunMiniMaxH3Harness(ctx, request.Intent, MiniMaxH3HarnessOptions{
		Client: a.options.Client, Submitter: a.options.Submitter, AdmissionScope: request.AdmissionScope,
		IdempotencyKey: request.IdempotencyKey, OutputDir: request.OutputDir, Resolution: a.options.Resolution,
		UseContextIR: a.options.UseContextIR, ContextIRPollAttempts: a.options.ContextIRPollAttempts,
		ContextIRPollInterval: a.options.ContextIRPollInterval, GenerationPollAttempts: a.options.GenerationPollAttempts,
		GenerationPollInterval: a.options.GenerationPollInterval, Timeout: request.Timeout,
		Downloader: a.options.Downloader, Normalizer: a.options.Normalizer, Now: a.options.Now,
	})
	result.ProviderTaskID = harness.GenerationTaskID
	result.Candidate = harness.Candidate
	result.StructuralReview = harness.StructuralReview
	result.Status = harness.Status
	result.ErrorClass = harness.ErrorClass
	result.ErrorMessage = harness.ErrorMessage
	if err != nil {
		return result, err
	}
	return result, nil
}

func (a *MiniMaxH3ProviderAdapter) Cancel(ctx context.Context, taskID string) error {
	if !a.options.Enabled || a.options.Canceller == nil {
		return errors.New("MiniMax-H3 cancellation is unavailable")
	}
	_, err := a.options.Canceller.CancelOrDeleteContentGenerationTask(ctx, taskID)
	return err
}
