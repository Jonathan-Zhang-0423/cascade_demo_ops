export type DemoUseCase =
  | "help_center"
  | "user_documentation"
  | "launch"
  | "sales"
  | "support"
  | "onboarding"
  | "investor_demo";

export type GraphStatus =
  | "draft"
  | "review_ready"
  | "approved"
  | "rehearsing"
  | "validated"
  | "asset_ready"
  | "deprecated";

export type GraphNodeType =
  | "start"
  | "action"
  | "decision"
  | "validation"
  | "narrative"
  | "capture"
  | "end";

export type GraphActionType =
  | "navigate"
  | "click"
  | "fill"
  | "select"
  | "upload"
  | "wait"
  | "assert"
  | "inspect"
  | "api_call";

export type AssetKind =
  | "demo_video"
  | "screenshot_pack"
  | "step_by_step_docs"
  | "interactive_demo"
  | "support_snippet"
  | "sales_material";

export type EvidenceKind =
  | "user_input"
  | "source_code"
  | "code_snapshot"
  | "requirement_doc"
  | "webpage_screenshot"
  | "screenshot_ocr"
  | "vision_finding"
  | "repo_snapshot"
  | "browser_scan"
  | "browser_trace"
  | "server_snapshot"
  | "docs"
  | "release_note"
  | "brand_kit"
  | "execution_run"
  | "asset_review";

export type ProjectInputBundle = {
  product_urls?: ProductURLInput[];
  code?: CodeInput[];
  repositories?: RepositoryInput[];
  requirement_documents?: RequirementDocumentInput[];
  webpage_screenshots?: WebpageScreenshotInput[];
  credentials?: CredentialInput[];
  knowledge_sources?: KnowledgeSource[];
  release_notes?: ReleaseNoteInput[];
  brand_kit?: BrandKit;
  scenarios?: DemoScenario[];
  requirements?: DemoRequirement[];
  raw_user_prompt?: string;
  metadata?: Record<string, unknown>;
};

export type ProductURLInput = {
  url: string;
  kind?: string;
  environment?: string;
  headers?: Record<string, string>;
  health_check?: string;
};

export type CodeInput = {
  id: string;
  kind: string;
  uri?: string;
  local_path?: string;
  repository_id?: string;
  branch?: string;
  commit_sha?: string;
  language?: string;
  framework?: string;
  entrypoints?: string[];
  include_globs?: string[];
  exclude_globs?: string[];
  artifact?: ArtifactRef;
  snippet?: string;
  read_only: boolean;
  evidence_refs?: EvidenceRef[];
  metadata?: Record<string, unknown>;
};

export type RepositoryInput = {
  url?: string;
  local_path?: string;
  provider?: string;
  branch?: string;
  read_only: boolean;
  secret_ref?: string;
  primary?: boolean;
  last_snapshot_id?: string;
};

export type RequirementDocumentInput = {
  id: string;
  kind: string;
  title?: string;
  uri?: string;
  local_path?: string;
  artifact?: ArtifactRef;
  body?: string;
  version?: string;
  author?: string;
  updated_at?: string;
  focus_areas?: string[];
  requirement_ids?: string[];
  evidence_refs?: EvidenceRef[];
  metadata?: Record<string, unknown>;
};

export type WebpageScreenshotInput = {
  id: string;
  url?: string;
  title?: string;
  page_role?: string;
  artifact: ArtifactRef;
  viewport?: ViewportSpec;
  captured_at?: string;
  sequence_id?: string;
  step_hint?: string;
  annotations?: ScreenshotAnnotation[];
  ocr_text?: string;
  vision_summary?: string;
  evidence_refs?: EvidenceRef[];
  metadata?: Record<string, unknown>;
};

export type ScreenshotAnnotation = {
  id: string;
  kind: string;
  label?: string;
  description?: string;
  bounds?: CropRect;
  selector_hint?: string;
  feature_ref?: string;
};

export type CredentialInput = {
  id: string;
  kind: string;
  secret_ref: string;
  scope?: string;
  expires_at?: string;
  required_for?: string[];
  session_policy?: string;
};

export type KnowledgeSource = {
  id: string;
  kind: EvidenceKind;
  uri?: string;
  title?: string;
  required?: boolean;
  evidence?: EvidenceRef;
  metadata?: Record<string, unknown>;
};

export type ReleaseNoteInput = {
  id: string;
  version?: string;
  title?: string;
  body?: string;
  url?: string;
  released_at?: string;
  feature_refs?: string[];
  evidence_refs?: EvidenceRef[];
};

export type DemoScenario = {
  id: string;
  use_case: DemoUseCase;
  audience_id?: string;
  objective?: string;
  primary_outcome?: string;
  duration_seconds?: number;
  priority?: number;
  must_show?: string[];
  must_avoid?: string[];
};

export type DemoRequirement = {
  id: string;
  kind: string;
  description: string;
  required: boolean;
  applies_to?: DemoUseCase[];
  evidence_refs?: EvidenceRef[];
};

export type DemoWorkflowGraph = {
  id: string;
  project_id?: string;
  schema_version?: string;
  version: number;
  status?: GraphStatus;
  name?: string;
  summary?: string;
  entry_point: string;
  intent?: WorkflowIntent;
  requirements?: GraphRequirement[];
  variables?: GraphVariable[];
  test_data?: TestDataRecord[];
  nodes: GraphNode[];
  edges: GraphEdge[];
  states?: GraphState[];
  validations?: ValidationSpec[];
  narratives?: NarrativeSegment[];
  assets: AssetManifest;
  evidence_refs?: EvidenceRef[];
  provenance?: GraphProvenance;
  review?: GraphReviewState;
  execution?: ExecutionPolicy;
  maintenance?: MaintenancePolicy;
  created_at?: string;
  updated_at?: string;
};

export type WorkflowIntent = {
  use_case?: DemoUseCase;
  audience?: AudienceProfile;
  objective?: string;
  value_proposition?: string;
  primary_feature_refs?: string[];
  success_criteria?: string[];
  desired_emotion?: string;
  cta?: string;
};

export type AudienceProfile = {
  id: string;
  name: string;
  segment?: string;
  role?: string;
  expertise_level?: string;
  primary_jobs?: string[];
  pain_points?: string[];
  value_drivers?: string[];
  preferred_tone?: string;
  narrative_lens?: string;
  metadata?: Record<string, string>;
};

export type GraphRequirement = {
  id: string;
  kind: string;
  description: string;
  required: boolean;
  node_refs?: string[];
  evidence_refs?: EvidenceRef[];
};

export type GraphVariable = {
  name: string;
  kind?: string;
  value?: string;
  secret_ref?: string;
  required?: boolean;
  sensitive?: boolean;
  evidence_refs?: EvidenceRef[];
};

export type TestDataRecord = {
  id: string;
  name?: string;
  purpose?: string;
  fields?: Record<string, string>;
  secret_refs?: Record<string, string>;
  reset_policy?: string;
  evidence_refs?: EvidenceRef[];
};

export type GraphNode = {
  id: string;
  action: string;
  selector: string;
  input_data: string;
  expected_outcome: string;
  is_screenshot: boolean;
  has_zoom: boolean;
  retry_policy: number;
  type?: GraphNodeType;
  title?: string;
  goal?: string;
  description?: string;
  actor_role?: string;
  page_ref?: string;
  feature_refs?: string[];
  action_spec?: GraphAction;
  state_before?: StateAssertion[];
  state_after?: StateAssertion[];
  validations?: ValidationSpec[];
  narrative?: NarrativeCue;
  capture?: CaptureSpec;
  assets?: AssetRef[];
  evidence_refs?: EvidenceRef[];
  alternatives?: AlternativePath[];
  failure_policy?: NodeFailurePolicy;
  duration_hint_ms?: number;
  sensitive?: boolean;
  tags?: string[];
  metadata?: Record<string, unknown>;
};

export type GraphAction = {
  type: GraphActionType;
  target: ActionTarget;
  value?: string;
  input_ref?: string;
  secret_ref?: string;
  parameters?: Record<string, unknown>;
  timeout_ms?: number;
  wait_until?: string;
  preconditions?: StateAssertion[];
};

export type ActionTarget = {
  url?: string;
  selector?: string;
  selector_alternatives?: SelectorCandidate[];
  role?: string;
  text?: string;
  label?: string;
  test_id?: string;
  frame?: string;
  component_ref?: string;
  evidence_refs?: EvidenceRef[];
};

export type SelectorCandidate = {
  kind: string;
  value: string;
  confidence?: number;
  stability_score?: number;
  source?: string;
  last_validated_at?: string;
  evidence_refs?: EvidenceRef[];
};

export type GraphState = {
  id: string;
  name: string;
  kind?: string;
  url_pattern?: string;
  dom_hints?: SelectorCandidate[];
  data_assertions?: StateAssertion[];
  feature_refs?: string[];
  evidence_refs?: EvidenceRef[];
};

export type StateAssertion = {
  id?: string;
  kind: string;
  target?: ActionTarget;
  operator?: string;
  expected?: unknown;
  required: boolean;
  timeout_ms?: number;
  evidence_refs?: EvidenceRef[];
};

export type ValidationSpec = {
  id: string;
  kind: string;
  target?: ActionTarget;
  assertion?: string;
  expected?: unknown;
  severity?: string;
  required: boolean;
  repair_policy?: RepairPolicy;
  evidence_refs?: EvidenceRef[];
};

export type RepairPolicy = {
  allow_selector_repair: boolean;
  allow_data_repair: boolean;
  allow_step_skip: boolean;
  max_attempts?: number;
  escalate_to_human_on?: string[];
};

export type NarrativeSegment = {
  id: string;
  node_refs?: string[];
  title?: string;
  summary?: string;
  voiceover?: string;
  caption?: string;
  tone?: string;
  audience_lens?: string;
  timing?: TimingHint;
  evidence_refs?: EvidenceRef[];
};

export type NarrativeCue = {
  title?: string;
  voiceover?: string;
  caption?: string;
  callout?: string;
  tone?: string;
  audience_lens?: string;
  timing?: TimingHint;
};

export type TimingHint = {
  start_ms?: number;
  duration_ms?: number;
  order?: number;
};

export type CaptureSpec = {
  screenshot: boolean;
  video?: boolean;
  zoom?: boolean;
  callout?: boolean;
  focus_selector?: string;
  asset_role?: string;
  crop?: CropRect;
  mask_selectors?: string[];
  redactions?: RedactionSpec[];
};

export type CropRect = {
  x: number;
  y: number;
  width: number;
  height: number;
};

export type RedactionSpec = {
  selector?: string;
  pattern?: string;
  replacement?: string;
  reason?: string;
};

export type AssetRef = {
  id: string;
  kind: AssetKind;
  uri?: string;
  source_node_id?: string;
  execution_run_id?: string;
  evidence_refs?: EvidenceRef[];
  provenance?: AssetProvenance;
};

export type AlternativePath = {
  id: string;
  reason?: string;
  action?: GraphAction;
  node_refs?: string[];
  evidence_refs?: EvidenceRef[];
};

export type NodeFailurePolicy = {
  retry_attempts?: number;
  retry_backoff_ms?: number;
  on_failure?: string;
  repair_policy?: RepairPolicy;
  human_review_required?: boolean;
};

export type GraphEdge = {
  id: string;
  from_node: string;
  to_node: string;
  condition?: string;
  condition_spec?: EdgeCondition;
  priority?: number;
  evidence_refs?: EvidenceRef[];
};

export type EdgeCondition = {
  kind: string;
  expression?: string;
  pass_state?: string;
  fail_state?: string;
};

export type AssetManifest = {
  demo_video_60s: boolean;
  screenshot_pack: boolean;
  step_by_step_docs: boolean;
  interactive_demo?: boolean;
  support_snippet?: boolean;
  sales_material?: boolean;
  target_duration_sec: number;
  requested_assets?: AssetRequest[];
  generated_assets?: AssetRef[];
  channels?: DistributionChannel[];
  brand?: BrandKit;
  localization?: string[];
  review_status?: string;
  provenance?: AssetProvenance;
};

export type AssetRequest = {
  id: string;
  kind: AssetKind;
  use_case?: DemoUseCase;
  audience_id?: string;
  format?: string;
  duration_sec?: number;
  source_node_ids?: string[];
  required: boolean;
  status?: string;
  evidence_refs?: EvidenceRef[];
};

export type DistributionChannel = {
  kind: string;
  destination?: string;
  metadata?: Record<string, string>;
};

export type BrandKit = {
  id?: string;
  name?: string;
  logo_refs?: ArtifactRef[];
  primary_colors?: string[];
  accent_colors?: string[];
  font_families?: string[];
  voice_and_tone?: string;
  forbidden_phrases?: string[];
  style_notes?: string[];
  metadata?: Record<string, string>;
};

export type AssetProvenance = {
  workflow_graph_id?: string;
  graph_version?: number;
  execution_run_id?: string;
  repo_snapshot_id?: string;
  server_snapshot_id?: string;
  evidence_refs?: EvidenceRef[];
  generated_by?: string;
  generated_at?: string;
};

export type GraphProvenance = {
  created_by?: string;
  model?: string;
  prompt_ref?: string;
  product_map_id?: string;
  repo_snapshot_id?: string;
  browser_scan_id?: string;
  evidence_refs?: EvidenceRef[];
};

export type GraphReviewState = {
  status?: string;
  reviewed_by?: string;
  reviewed_at?: string;
  notes?: string[];
  change_refs?: string[];
};

export type ExecutionPolicy = {
  required_pass_rate?: number;
  max_attempts?: number;
  timeout_ms?: number;
  browser?: string;
  headless?: boolean;
  viewports?: ViewportSpec[];
  trace_level?: string;
  failure_policy?: RepairPolicy;
};

export type ViewportSpec = {
  name?: string;
  width: number;
  height: number;
  device?: string;
};

export type MaintenancePolicy = {
  update_triggers?: string[];
  staleness_days?: number;
  last_verified_at?: string;
  owner?: string;
  regression_checks?: string[];
  source_evidence_refs?: EvidenceRef[];
};

export type EvidenceRef = {
  id: string;
  kind?: EvidenceKind;
  summary?: string;
  field_path?: string;
  artifact_id?: string;
  confidence?: number;
};

export type ArtifactRef = {
  id: string;
  kind?: string;
  uri: string;
  mime_type?: string;
  label?: string;
  sha256?: string;
  size_bytes?: number;
  created_at?: string;
  sensitive?: boolean;
  source_node_id?: string;
};
