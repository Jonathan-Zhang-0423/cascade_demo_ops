package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const MiniMaxH3HarnessSchemaVersion = "demoops.minimax_h3_harness.v1"

type MiniMaxH3HarnessClient interface {
	CreateH3ContextIRTask(context.Context, ContentGenerationTaskRequest) (ContentGenerationTaskResult, error)
	CreateContentGenerationTask(context.Context, ContentGenerationTaskRequest) (ContentGenerationTaskResult, error)
	GetContentGenerationTask(context.Context, string) (ContentGenerationTaskResult, error)
}

type MiniMaxH3HarnessSubmitter interface {
	Submit(context.Context, MiniMaxH3GovernedSubmitRequest) (MiniMaxH3GovernedSubmitResult, error)
}

type MiniMaxH3HarnessOptions struct {
	Client                      MiniMaxH3HarnessClient
	Submitter                   MiniMaxH3HarnessSubmitter
	AdmissionScope              string
	IdempotencyKey              string
	OutputDir                   string
	Resolution                  string
	UseContextIR                bool
	ContextIRPollAttempts       int
	ContextIRPollInterval       time.Duration
	GenerationPollAttempts      int
	GenerationPollInterval      time.Duration
	ExistingGenerationTaskID    string
	Timeout                     time.Duration
	Downloader                  MiniMaxH3OutputDownloader
	Normalizer                  MiniMaxH3MediaNormalizer
	Now                         func() time.Time
	AllowUnpricedOperatorSubmit bool
}

type MiniMaxH3HarnessStage struct {
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	TaskID     string            `json:"task_id,omitempty"`
	CheckedAt  time.Time         `json:"checked_at"`
	ErrorClass string            `json:"error_class,omitempty"`
	Trace      ArkMediaCallTrace `json:"trace,omitempty"`
}

type MiniMaxH3HarnessResult struct {
	SchemaVersion          string                         `json:"schema_version"`
	Status                 string                         `json:"status"`
	IntentID               string                         `json:"intent_id"`
	Provider               string                         `json:"provider"`
	Model                  string                         `json:"model"`
	Resolution             string                         `json:"resolution"`
	FailurePolicy          string                         `json:"failure_policy"`
	CreatedAt              time.Time                      `json:"created_at"`
	OriginalPromptSHA256   string                         `json:"original_prompt_sha256"`
	EnhancedPromptSHA256   string                         `json:"enhanced_prompt_sha256,omitempty"`
	ContextIRTaskID        string                         `json:"context_ir_task_id,omitempty"`
	GenerationTaskID       string                         `json:"generation_task_id,omitempty"`
	Stages                 []MiniMaxH3HarnessStage        `json:"stages"`
	Pipeline               *MiniMaxH3PipelineResult       `json:"pipeline,omitempty"`
	Candidate              *GeneratedShotCandidate        `json:"candidate,omitempty"`
	StructuralReview       *GeneratedShotStructuralReview `json:"structural_review,omitempty"`
	RequiresContentReview  bool                           `json:"requires_content_review"`
	AutoIncludedInEditPlan bool                           `json:"auto_included_in_edit_plan"`
	ErrorClass             string                         `json:"error_class,omitempty"`
	ErrorMessage           string                         `json:"error_message,omitempty"`
}

// RunMiniMaxH3Harness executes the provider side of a presentation-only shot.
// It deliberately stops after deterministic structural review. A human content
// review, explicit selection, editor approval, edit-plan patch and deterministic
// render remain separate required steps.
func RunMiniMaxH3Harness(ctx context.Context, intent GeneratedShotIntent, options MiniMaxH3HarnessOptions) (MiniMaxH3HarnessResult, error) {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	result := MiniMaxH3HarnessResult{
		SchemaVersion: MiniMaxH3HarnessSchemaVersion, Status: "not_started", IntentID: strings.TrimSpace(intent.IntentID),
		Provider: GeneratedShotProviderMiniMaxH3, Model: MiniMaxH3Model, FailurePolicy: GeneratedShotFailureContinue,
		CreatedAt: now().UTC(), RequiresContentReview: true, AutoIncludedInEditPlan: false,
		OriginalPromptSHA256: miniMaxH3PromptDigest(intent.Prompt),
	}
	fail := func(class string, err error) (MiniMaxH3HarnessResult, error) {
		result.Status = GeneratedShotFailureContinue
		result.ErrorClass = strings.TrimSpace(class)
		if err != nil {
			result.ErrorMessage = err.Error()
		}
		return result, err
	}
	if options.Client == nil {
		return fail("client_missing", errors.New("MiniMax-H3 harness client is required"))
	}
	if strings.TrimSpace(options.OutputDir) == "" {
		return fail("output_dir_missing", errors.New("MiniMax-H3 harness output directory is required"))
	}
	if err := ValidateGeneratedShotIntent(intent); err != nil {
		return fail("intent_invalid", err)
	}
	existingTaskID := strings.TrimSpace(options.ExistingGenerationTaskID)
	if existingTaskID == "" && options.Submitter == nil && !options.AllowUnpricedOperatorSubmit {
		return fail("admission_required", errors.New("real H3 generation requires a governed production submitter or explicit operator preflight authorization"))
	}
	if existingTaskID == "" && options.Submitter != nil && options.UseContextIR && !options.AllowUnpricedOperatorSubmit {
		return fail("context_ir_admission_required", errors.New("production H3-Context-IR requires its own persisted quota policy; disable Context-IR or use an explicit operator preflight"))
	}
	compiled, err := (MiniMaxH3GeneratedShotCompiler{}).Compile(intent)
	if err != nil {
		return fail("request_compile_failed", err)
	}
	compiled.DryRunOnly = false
	compiled.Warnings = nil
	resolution := strings.ToUpper(strings.TrimSpace(options.Resolution))
	if resolution == "" {
		resolution = "768P"
	}
	compiled.Request.Resolution = resolution
	if err := validateMiniMaxH3Request(compiled.Request); err != nil {
		return fail("request_invalid", err)
	}
	result.Resolution = resolution

	if ctx == nil {
		ctx = context.Background()
	}
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}

	if options.UseContextIR && existingTaskID == "" {
		created, createErr := options.Client.CreateH3ContextIRTask(ctx, compiled.Request)
		stage := MiniMaxH3HarnessStage{Name: "h3_context_ir_create", CheckedAt: now().UTC(), Trace: created.Trace}
		if created.Response != nil {
			stage.TaskID = strings.TrimSpace(created.Response.ID)
		}
		if createErr != nil || stage.TaskID == "" {
			stage.Status = "failed"
			stage.ErrorClass = firstNonEmpty(created.Trace.ErrorClass, "context_ir_create_failed")
			result.Stages = append(result.Stages, stage)
			if createErr == nil {
				createErr = errors.New("MiniMax-H3 Context-IR task id is missing")
			}
			return fail(stage.ErrorClass, createErr)
		}
		stage.Status = "submitted"
		result.ContextIRTaskID = stage.TaskID
		result.Stages = append(result.Stages, stage)

		enhancedPrompt, queryStages, queryErr := waitForMiniMaxH3EnhancedPrompt(ctx, options.Client, stage.TaskID, options.ContextIRPollAttempts, options.ContextIRPollInterval, now)
		result.Stages = append(result.Stages, queryStages...)
		if queryErr != nil {
			return fail("context_ir_failed", queryErr)
		}
		result.EnhancedPromptSHA256 = miniMaxH3PromptDigest(enhancedPrompt)
		for index := range compiled.Request.Content {
			if compiled.Request.Content[index].Type == "text" {
				compiled.Request.Content[index].Text = enhancedPrompt
				break
			}
		}
	}

	createStage := MiniMaxH3HarnessStage{Name: "video_generation_resume", Status: "resumed", TaskID: existingTaskID, CheckedAt: now().UTC()}
	if existingTaskID == "" {
		var created ContentGenerationTaskResult
		var createErr error
		if options.Submitter != nil {
			governed, governedErr := options.Submitter.Submit(ctx, MiniMaxH3GovernedSubmitRequest{
				Scope: strings.TrimSpace(options.AdmissionScope), IdempotencyKey: strings.TrimSpace(options.IdempotencyKey), Request: compiled.Request,
			})
			createErr = governedErr
			if governed.ProviderResult != nil {
				created = *governed.ProviderResult
			}
		} else {
			created, createErr = options.Client.CreateContentGenerationTask(ctx, compiled.Request)
		}
		createStage = MiniMaxH3HarnessStage{Name: "video_generation_create", CheckedAt: now().UTC(), Trace: created.Trace}
		if created.Response != nil {
			createStage.TaskID = strings.TrimSpace(created.Response.ID)
		}
		if createErr != nil || createStage.TaskID == "" {
			createStage.Status = "failed"
			createStage.ErrorClass = firstNonEmpty(created.Trace.ErrorClass, "generation_create_failed")
			result.Stages = append(result.Stages, createStage)
			if createErr == nil {
				createErr = errors.New("MiniMax-H3 generation task id is missing")
			}
			return fail(createStage.ErrorClass, createErr)
		}
		createStage.Status = "submitted"
	}
	result.GenerationTaskID = createStage.TaskID
	result.Stages = append(result.Stages, createStage)

	pipeline := CompleteMiniMaxH3Task(ctx, createStage.TaskID, MiniMaxH3PipelineOptions{
		Client: options.Client, Downloader: options.Downloader, Normalizer: options.Normalizer,
		OutputDir: options.OutputDir, PollAttempts: options.GenerationPollAttempts,
		PollInterval: options.GenerationPollInterval, Timeout: options.Timeout, Now: now,
	})
	result.Pipeline = &pipeline
	if pipeline.Status != GeneratedShotCandidateReadyForReview {
		err := errors.New(firstNonEmpty(pipeline.ErrorMessage, "MiniMax-H3 generation did not produce a normalized candidate"))
		return fail(firstNonEmpty(pipeline.ErrorClass, pipeline.Status), err)
	}
	candidateID := "h3_" + safeMiniMaxH3PathPart(intent.IntentID) + "_" + safeMiniMaxH3PathPart(createStage.TaskID)
	candidate, err := NewGeneratedShotCandidateFromMiniMaxH3(candidateID, intent.IntentID, pipeline)
	if err != nil {
		return fail("candidate_conversion_failed", err)
	}
	review := ReviewGeneratedShotCandidateStructure("structural_"+candidateID, intent, candidate)
	result.Candidate = &candidate
	result.StructuralReview = &review
	if !review.StructurallyEligible {
		return fail("structural_review_failed", fmt.Errorf("generated candidate failed structural review: %+v", review.Findings))
	}
	result.Status = "awaiting_human_content_review"
	return result, nil
}

func waitForMiniMaxH3EnhancedPrompt(ctx context.Context, client MiniMaxH3HarnessClient, taskID string, attempts int, interval time.Duration, now func() time.Time) (string, []MiniMaxH3HarnessStage, error) {
	if attempts <= 0 {
		attempts = 20
	}
	stages := make([]MiniMaxH3HarnessStage, 0, attempts)
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 && interval > 0 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", stages, ctx.Err()
			case <-timer.C:
			}
		}
		queried, err := client.GetContentGenerationTask(ctx, taskID)
		stage := MiniMaxH3HarnessStage{Name: "h3_context_ir_query", CheckedAt: now().UTC(), Trace: queried.Trace}
		status := ""
		if queried.Response != nil {
			stage.TaskID = queried.Response.ID
			status = normalizeMiniMaxH3TaskStatus(queried.Response.Status)
			stage.Status = status
		}
		if err != nil {
			stage.Status = "failed"
			stage.ErrorClass = firstNonEmpty(queried.Trace.ErrorClass, "context_ir_query_failed")
			stages = append(stages, stage)
			if attempt < attempts && miniMaxH3ErrorIsRetryable(stage.ErrorClass) {
				continue
			}
			return "", stages, err
		}
		stages = append(stages, stage)
		switch status {
		case MiniMaxH3TaskSucceeded:
			prompt, _ := queried.Response.Output["enhanced_prompt"].(string)
			prompt = strings.TrimSpace(prompt)
			if prompt == "" {
				return "", stages, errors.New("succeeded H3 Context-IR task is missing enhanced_prompt")
			}
			return prompt, stages, nil
		case MiniMaxH3TaskFailed, MiniMaxH3TaskCancelled, MiniMaxH3TaskExpired:
			return "", stages, fmt.Errorf("H3 Context-IR task ended with status %s", status)
		case MiniMaxH3TaskQueued, MiniMaxH3TaskRunning:
			continue
		default:
			return "", stages, fmt.Errorf("H3 Context-IR task returned unsupported status %q", status)
		}
	}
	return "", stages, errors.New("H3 Context-IR poll limit reached")
}

func miniMaxH3PromptDigest(prompt string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(prompt)))
	return hex.EncodeToString(hash[:])
}
