package experiment

import "time"

const (
	DefinitionSchemaVersion          = "demoops.experiment_definition.v1"
	ProductSpecSchemaVersion         = "demoops.product_spec_artifact.v1"
	ObservationPlanSchemaVersion     = "demoops.build_observation_plan.v1"
	InteractionPlanSchemaVersion     = "demoops.interaction_evidence_plan.v1"
	RunSchemaVersion                 = "demoops.experiment_run.v1"
	EventSchemaVersion               = "demoops.execution_event.v1"
	ReportSchemaVersion              = "demoops.experiment_run_report.v1"
	WorkflowTemplateAsyncProductDemo = "async-product-build-demo-v1"
	MaxEventBodyBytes                = 64 * 1024
)

type RunState string

const (
	RunStateCreated         RunState = "created"
	RunStateAdmitted        RunState = "admitted"
	RunStateQueued          RunState = "queued"
	RunStateRunning         RunState = "running"
	RunStateWaitingInput    RunState = "waiting_input"
	RunStateWaitingExternal RunState = "waiting_external"
	RunStateSucceeded       RunState = "succeeded"
	RunStateFailed          RunState = "failed"
	RunStateCanceled        RunState = "canceled"
	RunStateExpired         RunState = "expired"
)

type ReplayPolicy string

const (
	ReplayObserveOnly     ReplayPolicy = "observe_only"
	ReplayIdempotentWrite ReplayPolicy = "idempotent_write"
	ReplayOnceEffect      ReplayPolicy = "once_effect"
)

type Definition struct {
	SchemaVersion          string              `json:"schema_version"`
	DefinitionID           string              `json:"definition_id"`
	WorkflowTemplateID     string              `json:"workflow_template_id"`
	ShortGoal              string              `json:"short_goal"`
	MainProjectName        string              `json:"main_project_name"`
	RecoveryProjectName    string              `json:"recovery_project_name"`
	ProductSpecRef         string              `json:"product_spec_ref"`
	ObservationPlanRef     string              `json:"build_observation_plan_ref"`
	InteractionPlanRef     string              `json:"interaction_evidence_plan_ref"`
	RunSet                 []string            `json:"run_set"`
	RecoveryInjectionPhase string              `json:"recovery_injection_phase"`
	MainTargetDurationMS   DurationRange       `json:"main_target_duration_ms"`
	AuthorizationBudget    AuthorizationBudget `json:"authorization_budget"`
}

type DurationRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type AuthorizationBudget struct {
	TargetSubmissions int `json:"target_submissions"`
	FinalFilmJobs     int `json:"final_film_jobs"`
	ProviderCalls     int `json:"provider_calls"`
	VisualCallsPerRun int `json:"visual_calls_per_run"`
}

type ProductSpec struct {
	SchemaVersion           string                `json:"schema_version"`
	SpecID                  string                `json:"spec_id"`
	Title                   string                `json:"title"`
	Objective               string                `json:"objective"`
	Audience                string                `json:"audience"`
	Requirements            []ProductRequirement  `json:"requirements"`
	VisualDirection         VisualDirection       `json:"visual_direction"`
	InteractionRequirements []ProductRequirement  `json:"interaction_requirements"`
	ResponsiveRequirements  []string              `json:"responsive_requirements,omitempty"`
	ObservableAcceptance    []AcceptanceCriterion `json:"observable_acceptance"`
	ForbiddenOutcomes       []string              `json:"forbidden_outcomes"`
}

type ProductRequirement struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	Priority  string `json:"priority"`
}

type VisualDirection struct {
	Theme   string   `json:"theme"`
	Palette []string `json:"palette"`
	Motion  string   `json:"motion"`
}

type AcceptanceCriterion struct {
	ID            string   `json:"id"`
	Statement     string   `json:"statement"`
	EvidenceKinds []string `json:"evidence_kinds"`
	Required      bool     `json:"required"`
}

type ObservationPlan struct {
	SchemaVersion            string                  `json:"schema_version"`
	PlanID                   string                  `json:"plan_id"`
	InitialPhase             string                  `json:"initial_phase"`
	TerminalPhase            string                  `json:"terminal_phase"`
	Transitions              []ObservationTransition `json:"transitions"`
	HeartbeatMS              int                     `json:"heartbeat_ms"`
	WarnAfterMS              int                     `json:"warn_after_ms"`
	DeferAfterMS             int                     `json:"defer_after_ms"`
	MaxVisualCalls           int                     `json:"max_visual_calls"`
	RequiredTerminalChannels int                     `json:"required_terminal_channels"`
	FailureSignals           []string                `json:"failure_signals,omitempty"`
}

type ObservationTransition struct {
	From          string   `json:"from"`
	To            string   `json:"to"`
	EvidenceKinds []string `json:"evidence_kinds"`
}

type InteractionPlan struct {
	SchemaVersion string            `json:"schema_version"`
	PlanID        string            `json:"plan_id"`
	SurfaceKind   string            `json:"surface_kind"`
	Steps         []InteractionStep `json:"steps"`
}

type InteractionStep struct {
	StepID            string             `json:"step_id"`
	SemanticIntent    string             `json:"semantic_intent"`
	ReplayPolicy      ReplayPolicy       `json:"replay_policy"`
	Preconditions     []string           `json:"preconditions"`
	ExpectedChanges   []string           `json:"expected_changes"`
	EvidenceSlots     []string           `json:"evidence_slots"`
	ProofRequirements []ProofRequirement `json:"proof_requirements"`
	MaxAttempts       int                `json:"max_attempts,omitempty"`
}

type ProofRequirement struct {
	Kind          string  `json:"kind"`
	MinCount      int     `json:"min_count,omitempty"`
	Modality      string  `json:"modality,omitempty"`
	MinSimilarity float64 `json:"min_similarity,omitempty"`
}

type CreateRunRequest struct {
	DefinitionRef    string `json:"definition_ref"`
	TargetURL        string `json:"target_url"`
	CredentialRef    string `json:"credential_ref"`
	AuthorizationRef string `json:"authorization_ref"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type Run struct {
	SchemaVersion     string              `json:"schema_version"`
	RunID             string              `json:"run_id"`
	DefinitionRef     string              `json:"definition_ref"`
	DefinitionID      string              `json:"definition_id"`
	WorkflowTemplate  string              `json:"workflow_template_id"`
	TargetURL         string              `json:"target_url"`
	CredentialRef     string              `json:"credential_ref"`
	AuthorizationRef  string              `json:"authorization_ref"`
	IdempotencyKey    string              `json:"idempotency_key"`
	State             RunState            `json:"state"`
	Phase             string              `json:"phase"`
	Revision          int                 `json:"revision"`
	Budget            AuthorizationBudget `json:"budget"`
	ProductSpec       ProductSpec         `json:"product_spec"`
	ObservationPlan   ObservationPlan     `json:"observation_plan"`
	InteractionPlan   InteractionPlan     `json:"interaction_plan"`
	Legs              []RunLeg            `json:"legs"`
	ProviderCallsUsed int                 `json:"provider_calls_used"`
	FinalFilm         *FinalFilmBinding   `json:"final_film,omitempty"`
	Report            *RunReport          `json:"report,omitempty"`
	Waiting           *WaitingState       `json:"waiting,omitempty"`
	LastError         *RunError           `json:"last_error,omitempty"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
	TerminalAt        time.Time           `json:"terminal_at,omitempty"`
}

type RunLeg struct {
	LegID              string              `json:"leg_id"`
	Kind               string              `json:"kind"`
	ProjectName        string              `json:"project_name"`
	BuildPrompt        string              `json:"build_prompt"`
	State              RunState            `json:"state"`
	Phase              string              `json:"phase"`
	BrowserAttempt     int                 `json:"browser_attempt"`
	VisualCallsUsed    int                 `json:"visual_calls_used"`
	TargetSubmissions  int                 `json:"target_submissions"`
	Checkpoint         *Checkpoint         `json:"checkpoint,omitempty"`
	ArtifactRefs       []ArtifactRef       `json:"artifact_refs,omitempty"`
	HumanInterventions []HumanIntervention `json:"human_interventions,omitempty"`
}

type Checkpoint struct {
	CheckpointID        string             `json:"checkpoint_id"`
	VerifiedPhase       string             `json:"verified_phase"`
	StateFingerprintRef string             `json:"state_fingerprint_ref,omitempty"`
	ResultEntryRef      string             `json:"result_entry_ref,omitempty"`
	SegmentRefs         []ArtifactRef      `json:"segment_refs,omitempty"`
	OnceEffects         []OnceEffectRecord `json:"once_effect_records"`
	CreatedAt           time.Time          `json:"created_at"`
}

type OnceEffectRecord struct {
	EffectID        string    `json:"effect_id"`
	Kind            string    `json:"kind"`
	Status          string    `json:"status"`
	IdempotencyKey  string    `json:"idempotency_key"`
	EvidenceRefs    []string  `json:"evidence_refs,omitempty"`
	ExternalTaskRef string    `json:"external_task_ref,omitempty"`
	StartedAt       time.Time `json:"started_at,omitempty"`
	ConfirmedAt     time.Time `json:"confirmed_at,omitempty"`
}

type ArtifactRef struct {
	ArtifactID string `json:"artifact_id"`
	Revision   int    `json:"revision"`
	Role       string `json:"role"`
}

type FinalFilmBinding struct {
	JobID     string `json:"job_id"`
	Revision  int    `json:"revision"`
	PackageID string `json:"package_id,omitempty"`
	Decision  string `json:"decision,omitempty"`
}

type WaitingState struct {
	Reason         string `json:"reason"`
	Responsibility string `json:"responsibility"`
	NextAction     string `json:"next_action"`
}

type RunError struct {
	Code           string   `json:"code"`
	Message        string   `json:"message"`
	Retryable      bool     `json:"retryable"`
	Responsibility string   `json:"responsibility"`
	EvidenceRefs   []string `json:"evidence_refs,omitempty"`
}

type Event struct {
	SchemaVersion string    `json:"schema_version"`
	EventID       string    `json:"event_id"`
	RunID         string    `json:"run_id"`
	Sequence      int       `json:"sequence"`
	Type          string    `json:"type"`
	State         RunState  `json:"state"`
	Phase         string    `json:"phase"`
	LegID         string    `json:"leg_id,omitempty"`
	Summary       string    `json:"summary"`
	EvidenceRefs  []string  `json:"evidence_refs,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type HumanIntervention struct {
	Kind       string    `json:"kind"`
	Allowed    bool      `json:"allowed"`
	OccurredAt time.Time `json:"occurred_at,omitempty"`
}

type RunReport struct {
	SchemaVersion      string                `json:"schema_version"`
	RunID              string                `json:"run_id"`
	Valid              bool                  `json:"valid"`
	Scores             map[string]float64    `json:"scores"`
	HumanInterventions []HumanIntervention   `json:"human_interventions"`
	ModelConsumption   ModelConsumption      `json:"model_consumption"`
	OnceEffects        []OnceEffectRecord    `json:"once_effect_records"`
	ArtifactRefs       []ArtifactRef         `json:"artifact_refs"`
	FailureReasons     []string              `json:"failure_reasons,omitempty"`
	Automation         AutomationAttribution `json:"automation_attribution"`
}

type ModelConsumption struct {
	VisualCalls   int `json:"visual_calls"`
	ProviderCalls int `json:"provider_calls"`
}

type AutomationAttribution struct {
	OutOfBandBrowserActions int `json:"out_of_band_browser_actions"`
	AllowedHumanGates       int `json:"allowed_human_gates"`
	SystemActions           int `json:"system_actions"`
}
