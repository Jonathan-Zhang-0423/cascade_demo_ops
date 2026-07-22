package model

import "time"

type AppMode string

const (
	AppModeWeb     AppMode = "web"
	AppModeDesktop AppMode = "desktop"
)

const ProjectContextSchemaVersion = "demoops.project_context.v1"

type DemoUseCase string

const (
	DemoUseCaseHelpCenter        DemoUseCase = "help_center"
	DemoUseCaseUserDocumentation DemoUseCase = "user_documentation"
	DemoUseCaseLaunch            DemoUseCase = "launch"
	DemoUseCaseSales             DemoUseCase = "sales"
	DemoUseCaseSupport           DemoUseCase = "support"
	DemoUseCaseOnboarding        DemoUseCase = "onboarding"
	DemoUseCaseInvestor          DemoUseCase = "investor_demo"
)

// ProjectContext is the full MVP input contract consumed by the graph flow.
// GitRepoURL and LocalRepoPath are parallel optional code sources; callers may
// provide either one or both alongside ProductURL.
type ProjectContext struct {
	ID                  string                   `json:"id"`
	SchemaVersion       string                   `json:"schema_version,omitempty"`
	Mode                AppMode                  `json:"mode"`
	Name                string                   `json:"name,omitempty"`
	ProductURL          string                   `json:"product_url"`
	DemoAccount         *DemoAccount             `json:"demo_account,omitempty"`
	GitRepoURL          string                   `json:"git_repo_url,omitempty"`
	LocalRepoPath       string                   `json:"local_repo_path,omitempty"`
	ServerAccess        *ServerAccess            `json:"server_access,omitempty"`
	ProductDescription  string                   `json:"product_description,omitempty"`
	TargetAudience      string                   `json:"target_audience"`
	BrandTone           string                   `json:"brand_tone,omitempty"`
	MustShow            []string                 `json:"must_show"`
	MustNotShow         []string                 `json:"must_not_show"`
	ForbiddenPages      []string                 `json:"forbidden_pages"`
	ForbiddenData       []string                 `json:"forbidden_data"`
	Inputs              *ProjectInputBundle      `json:"inputs,omitempty"`
	ProjectIntelligence *ProjectIntelligencePack `json:"project_intelligence,omitempty"`
	Goals               []DemoGoal               `json:"goals,omitempty"`
	Audiences           []AudienceProfile        `json:"audiences,omitempty"`
	BrandKit            *BrandKit                `json:"brand_kit,omitempty"`
	AccessPolicy        *AccessPolicy            `json:"access_policy,omitempty"`
	SecurityPolicy      *SecurityPolicy          `json:"security_policy,omitempty"`
	KnowledgeRefs       []EvidenceRef            `json:"knowledge_refs,omitempty"`
	CreatedAt           time.Time                `json:"created_at,omitempty"`
	UpdatedAt           time.Time                `json:"updated_at,omitempty"`
}

type DemoAccount struct {
	UsernameSecretRef string `json:"username_secret_ref"`
	PasswordSecretRef string `json:"password_secret_ref"`
	Provider          string `json:"provider,omitempty"`
	ExpiresAt         string `json:"expires_at,omitempty"`
	Scope             string `json:"scope,omitempty"`
	SessionRef        string `json:"session_ref,omitempty"`
}

type ServerAccess struct {
	Host                string   `json:"host"`
	Port                int      `json:"port"`
	UsernameSecretRef   string   `json:"username_secret_ref"`
	PrivateKeySecretRef string   `json:"private_key_secret_ref,omitempty"`
	PasswordSecretRef   string   `json:"password_secret_ref,omitempty"`
	AllowedPaths        []string `json:"allowed_paths"`
	AllowedCommands     []string `json:"allowed_commands"`
}

type ProjectInputBundle struct {
	ProductURLs          []ProductURLInput          `json:"product_urls,omitempty"`
	Code                 []CodeInput                `json:"code,omitempty"`
	Repositories         []RepositoryInput          `json:"repositories,omitempty"`
	RequirementDocuments []RequirementDocumentInput `json:"requirement_documents,omitempty"`
	WebpageScreenshots   []WebpageScreenshotInput   `json:"webpage_screenshots,omitempty"`
	Credentials          []CredentialInput          `json:"credentials,omitempty"`
	KnowledgeSources     []KnowledgeSource          `json:"knowledge_sources,omitempty"`
	ReleaseNotes         []ReleaseNoteInput         `json:"release_notes,omitempty"`
	BrandKit             *BrandKit                  `json:"brand_kit,omitempty"`
	Scenarios            []DemoScenario             `json:"scenarios,omitempty"`
	Requirements         []DemoRequirement          `json:"requirements,omitempty"`
	RawUserPrompt        string                     `json:"raw_user_prompt,omitempty"`
	Metadata             map[string]any             `json:"metadata,omitempty"`
}

type ProductURLInput struct {
	URL         string            `json:"url"`
	Kind        string            `json:"kind,omitempty"`
	Environment string            `json:"environment,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	HealthCheck string            `json:"health_check,omitempty"`
}

type RepositoryInput struct {
	URL            string `json:"url,omitempty"`
	LocalPath      string `json:"local_path,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Branch         string `json:"branch,omitempty"`
	ReadOnly       bool   `json:"read_only"`
	SecretRef      string `json:"secret_ref,omitempty"`
	Primary        bool   `json:"primary,omitempty"`
	LastSnapshotID string `json:"last_snapshot_id,omitempty"`
}

type CodeInput struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind"`
	URI          string         `json:"uri,omitempty"`
	LocalPath    string         `json:"local_path,omitempty"`
	RepositoryID string         `json:"repository_id,omitempty"`
	Branch       string         `json:"branch,omitempty"`
	CommitSHA    string         `json:"commit_sha,omitempty"`
	Language     string         `json:"language,omitempty"`
	Framework    string         `json:"framework,omitempty"`
	Entrypoints  []string       `json:"entrypoints,omitempty"`
	IncludeGlobs []string       `json:"include_globs,omitempty"`
	ExcludeGlobs []string       `json:"exclude_globs,omitempty"`
	Artifact     *ArtifactRef   `json:"artifact,omitempty"`
	Snippet      string         `json:"snippet,omitempty"`
	ReadOnly     bool           `json:"read_only"`
	EvidenceRefs []EvidenceRef  `json:"evidence_refs,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type RequirementDocumentInput struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	Title          string         `json:"title,omitempty"`
	URI            string         `json:"uri,omitempty"`
	LocalPath      string         `json:"local_path,omitempty"`
	Artifact       *ArtifactRef   `json:"artifact,omitempty"`
	Body           string         `json:"body,omitempty"`
	Version        string         `json:"version,omitempty"`
	Author         string         `json:"author,omitempty"`
	UpdatedAt      time.Time      `json:"updated_at,omitempty"`
	FocusAreas     []string       `json:"focus_areas,omitempty"`
	RequirementIDs []string       `json:"requirement_ids,omitempty"`
	EvidenceRefs   []EvidenceRef  `json:"evidence_refs,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type WebpageScreenshotInput struct {
	ID            string                 `json:"id"`
	URL           string                 `json:"url,omitempty"`
	Title         string                 `json:"title,omitempty"`
	PageRole      string                 `json:"page_role,omitempty"`
	Artifact      ArtifactRef            `json:"artifact"`
	Viewport      *ViewportSpec          `json:"viewport,omitempty"`
	CapturedAt    time.Time              `json:"captured_at,omitempty"`
	SequenceID    string                 `json:"sequence_id,omitempty"`
	StepHint      string                 `json:"step_hint,omitempty"`
	Annotations   []ScreenshotAnnotation `json:"annotations,omitempty"`
	OCRText       string                 `json:"ocr_text,omitempty"`
	VisionSummary string                 `json:"vision_summary,omitempty"`
	EvidenceRefs  []EvidenceRef          `json:"evidence_refs,omitempty"`
	Metadata      map[string]any         `json:"metadata,omitempty"`
}

type ScreenshotAnnotation struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	Label        string    `json:"label,omitempty"`
	Description  string    `json:"description,omitempty"`
	Bounds       *CropRect `json:"bounds,omitempty"`
	SelectorHint string    `json:"selector_hint,omitempty"`
	FeatureRef   string    `json:"feature_ref,omitempty"`
}

type CredentialInput struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	SecretRef     string    `json:"secret_ref"`
	Scope         string    `json:"scope,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	RequiredFor   []string  `json:"required_for,omitempty"`
	SessionPolicy string    `json:"session_policy,omitempty"`
}

type KnowledgeSource struct {
	ID       string         `json:"id"`
	Kind     EvidenceKind   `json:"kind"`
	URI      string         `json:"uri,omitempty"`
	Title    string         `json:"title,omitempty"`
	Required bool           `json:"required,omitempty"`
	Evidence EvidenceRef    `json:"evidence,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type ReleaseNoteInput struct {
	ID           string        `json:"id"`
	Version      string        `json:"version,omitempty"`
	Title        string        `json:"title,omitempty"`
	Body         string        `json:"body,omitempty"`
	URL          string        `json:"url,omitempty"`
	ReleasedAt   time.Time     `json:"released_at,omitempty"`
	FeatureRefs  []string      `json:"feature_refs,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type DemoScenario struct {
	ID              string      `json:"id"`
	UseCase         DemoUseCase `json:"use_case"`
	AudienceID      string      `json:"audience_id,omitempty"`
	Objective       string      `json:"objective,omitempty"`
	PrimaryOutcome  string      `json:"primary_outcome,omitempty"`
	DurationSeconds int         `json:"duration_seconds,omitempty"`
	Priority        int         `json:"priority,omitempty"`
	MustShow        []string    `json:"must_show,omitempty"`
	MustAvoid       []string    `json:"must_avoid,omitempty"`
}

type DemoRequirement struct {
	ID           string        `json:"id"`
	Kind         string        `json:"kind"`
	Description  string        `json:"description"`
	Required     bool          `json:"required"`
	AppliesTo    []DemoUseCase `json:"applies_to,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type DemoGoal struct {
	ID               string      `json:"id"`
	UseCase          DemoUseCase `json:"use_case"`
	AudienceID       string      `json:"audience_id,omitempty"`
	ValueProposition string      `json:"value_proposition,omitempty"`
	SuccessCriteria  []string    `json:"success_criteria,omitempty"`
	Priority         int         `json:"priority,omitempty"`
}

type AudienceProfile struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Segment        string            `json:"segment,omitempty"`
	Role           string            `json:"role,omitempty"`
	ExpertiseLevel string            `json:"expertise_level,omitempty"`
	PrimaryJobs    []string          `json:"primary_jobs,omitempty"`
	PainPoints     []string          `json:"pain_points,omitempty"`
	ValueDrivers   []string          `json:"value_drivers,omitempty"`
	PreferredTone  string            `json:"preferred_tone,omitempty"`
	NarrativeLens  string            `json:"narrative_lens,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type BrandKit struct {
	ID               string            `json:"id,omitempty"`
	Name             string            `json:"name,omitempty"`
	LogoRefs         []ArtifactRef     `json:"logo_refs,omitempty"`
	PrimaryColors    []string          `json:"primary_colors,omitempty"`
	AccentColors     []string          `json:"accent_colors,omitempty"`
	FontFamilies     []string          `json:"font_families,omitempty"`
	VoiceAndTone     string            `json:"voice_and_tone,omitempty"`
	ForbiddenPhrases []string          `json:"forbidden_phrases,omitempty"`
	StyleNotes       []string          `json:"style_notes,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type AccessPolicy struct {
	CredentialVaultRequired bool     `json:"credential_vault_required"`
	SessionIsolation        bool     `json:"session_isolation"`
	AutoExpireCredentials   bool     `json:"auto_expire_credentials"`
	DefaultCredentialTTLSec int      `json:"default_credential_ttl_sec,omitempty"`
	AllowedDomains          []string `json:"allowed_domains,omitempty"`
	AllowedRepoHosts        []string `json:"allowed_repo_hosts,omitempty"`
	AllowedCommands         []string `json:"allowed_commands,omitempty"`
	AllowedPaths            []string `json:"allowed_paths,omitempty"`
	AuditLogRequired        bool     `json:"audit_log_required"`
}

type SecurityPolicy struct {
	ForbiddenPages       []string `json:"forbidden_pages,omitempty"`
	ForbiddenData        []string `json:"forbidden_data,omitempty"`
	MaskSelectors        []string `json:"mask_selectors,omitempty"`
	PIIHandling          string   `json:"pii_handling,omitempty"`
	NetworkCapturePolicy string   `json:"network_capture_policy,omitempty"`
	DataResidency        string   `json:"data_residency,omitempty"`
}

type ProductMap struct {
	ID           string               `json:"id,omitempty"`
	ProjectID    string               `json:"project_id"`
	Version      int                  `json:"version,omitempty"`
	GeneratedAt  time.Time            `json:"generated_at,omitempty"`
	Pages        []*ProductPage       `json:"pages"`
	Features     []*Feature           `json:"features"`
	Routes       []*RouteMapNode      `json:"routes,omitempty"`
	Components   []*ComponentNode     `json:"components,omitempty"`
	DataModels   []*DataModelNode     `json:"data_models,omitempty"`
	Roles        []*ProductRole       `json:"roles,omitempty"`
	Workflows    []*WorkflowCandidate `json:"workflows,omitempty"`
	Glossary     []GlossaryTerm       `json:"glossary,omitempty"`
	EvidenceRefs []EvidenceRef        `json:"evidence_refs,omitempty"`
	Summary      string               `json:"summary,omitempty"`
}

type ProductPage struct {
	ID             string        `json:"id,omitempty"`
	URL            string        `json:"url"`
	RoutePattern   string        `json:"route_pattern,omitempty"`
	Title          string        `json:"title,omitempty"`
	Purpose        string        `json:"purpose,omitempty"`
	Actions        []string      `json:"actions"`
	PrimaryActions []UIActionRef `json:"primary_actions,omitempty"`
	States         []string      `json:"states,omitempty"`
	FeatureRefs    []string      `json:"feature_refs,omitempty"`
	EvidenceRefs   []EvidenceRef `json:"evidence_refs,omitempty"`
	DemoValueScore float64       `json:"demo_value_score"`
}

type Feature struct {
	ID              string        `json:"id,omitempty"`
	Name            string        `json:"name"`
	Kind            string        `json:"kind,omitempty"`
	UserValue       string        `json:"user_value"`
	BusinessValue   string        `json:"business_value,omitempty"`
	Priority        string        `json:"priority,omitempty"`
	BestAudience    []string      `json:"best_audience"`
	BestUseCases    []DemoUseCase `json:"best_use_cases,omitempty"`
	SupportingPages []string      `json:"supporting_pages,omitempty"`
	KeyActions      []string      `json:"key_actions,omitempty"`
	Dependencies    []string      `json:"dependencies,omitempty"`
	Risks           []string      `json:"risks,omitempty"`
	EvidenceIDs     []string      `json:"evidence_ids,omitempty"`
	EvidenceRefs    []EvidenceRef `json:"evidence_refs,omitempty"`
}

type RouteMapNode struct {
	ID           string        `json:"id"`
	Path         string        `json:"path"`
	Name         string        `json:"name,omitempty"`
	ParentID     string        `json:"parent_id,omitempty"`
	PageID       string        `json:"page_id,omitempty"`
	ComponentIDs []string      `json:"component_ids,omitempty"`
	AuthRequired bool          `json:"auth_required,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type ComponentNode struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Kind         string        `json:"kind,omitempty"`
	FilePath     string        `json:"file_path,omitempty"`
	Selectors    []string      `json:"selectors,omitempty"`
	Actions      []UIActionRef `json:"actions,omitempty"`
	FeatureRefs  []string      `json:"feature_refs,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type DataModelNode struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Kind         string        `json:"kind,omitempty"`
	Fields       []DataField   `json:"fields,omitempty"`
	SourcePath   string        `json:"source_path,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type DataField struct {
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"`
	Required  bool   `json:"required,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

type ProductRole struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Permissions  []string      `json:"permissions,omitempty"`
	FeatureRefs  []string      `json:"feature_refs,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type WorkflowCandidate struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	UseCase        DemoUseCase   `json:"use_case,omitempty"`
	AudienceID     string        `json:"audience_id,omitempty"`
	FeatureRefs    []string      `json:"feature_refs,omitempty"`
	PageRefs       []string      `json:"page_refs,omitempty"`
	EstimatedSteps int           `json:"estimated_steps,omitempty"`
	ValueScore     float64       `json:"value_score,omitempty"`
	Feasibility    float64       `json:"feasibility,omitempty"`
	RiskNotes      []string      `json:"risk_notes,omitempty"`
	EvidenceRefs   []EvidenceRef `json:"evidence_refs,omitempty"`
}

type UIActionRef struct {
	ID           string        `json:"id,omitempty"`
	Label        string        `json:"label,omitempty"`
	Kind         string        `json:"kind,omitempty"`
	Selector     string        `json:"selector,omitempty"`
	TargetRoute  string        `json:"target_route,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type GlossaryTerm struct {
	Term       string `json:"term"`
	Definition string `json:"definition"`
	FeatureRef string `json:"feature_ref,omitempty"`
}
