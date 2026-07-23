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

export type DataField = {
  name: string;
  type?: string;
  required?: boolean;
  sensitive?: boolean;
};

export type Feature = {
  id?: string;
  name: string;
  kind?: string;
  user_value: string;
  business_value?: string;
  priority?: string;
  best_audience: string[];
  best_use_cases?: DemoUseCase[];
  supporting_pages?: string[];
  key_actions?: string[];
  dependencies?: string[];
  risks?: string[];
  evidence_ids?: string[];
  evidence_refs?: EvidenceRef[];
};

export type WorkflowCandidate = {
  id: string;
  name: string;
  use_case?: DemoUseCase;
  audience_id?: string;
  feature_refs?: string[];
  page_refs?: string[];
  estimated_steps?: number;
  value_score?: number;
  feasibility?: number;
  risk_notes?: string[];
  evidence_refs?: EvidenceRef[];
};

export type PathDigest = {
  path_hash_sha256: string;
  content_sha256?: string;
  kind?: string;
  language?: string;
  entrypoint?: boolean;
};

export type RequirementBrief = {
  id: string;
  project_id?: string;
  schema_version?: string;
  scenario?: string;
  target_audience?: string;
  objective?: string;
  primary_outcome?: string;
  must_show?: string[];
  must_not_show?: string[];
  forbidden_pages?: string[];
  forbidden_data?: string[];
  use_cases?: DemoUseCase[];
  required_assets?: AssetKind[];
  brand_tone?: string;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
  created_at?: string;
};

export type CodeUnderstandingSnapshot = {
  id: string;
  project_id?: string;
  schema_version?: string;
  repository_id?: string;
  uri?: string;
  branch?: string;
  commit_sha?: string;
  languages?: string[];
  frameworks?: string[];
  entrypoint_hashes?: string[];
  routes?: RouteInsight[];
  components?: ComponentInsight[];
  selectors?: SelectorInsight[];
  api_endpoints?: APIEndpointInsight[];
  data_models?: DataModelInsight[];
  sensitive_fields?: SensitiveFieldFinding[];
  source_digest_sha256?: string;
  file_count?: number;
  read_budget?: CodeReadBudget;
  investigation_trace?: CodeInvestigationTrace;
  investigation_quality?: CodeInvestigationQualitySummary;
  path_digests?: PathDigest[];
  evidence_refs?: EvidenceRef[];
  summary?: string;
  created_at?: string;
};

export type CodeReadBudget = {
  mode?: string;
  repo_index_file_limit?: number;
  drilldown_rounds?: number;
  files_per_round?: number;
  total_file_limit?: number;
  max_file_bytes?: number;
  tool_search_file_limit?: number;
  tool_search_bytes_per_file?: number;
  tool_search_result_limit?: number;
};

export type CodeInvestigationQualitySummary = {
  mode?: string;
  tool_driven?: boolean;
  tool_call_count?: number;
  specialized_tool_call_count?: number;
  shell_run_tool_call_count?: number;
  total_files_discovered?: number;
  total_files_searched?: number;
  total_files_selected?: number;
  structured_file_count?: number;
  selected_file_ratio?: number;
  search_file_ratio?: number;
  answered_question_count?: number;
  open_question_count?: number;
  remaining_gaps?: string[];
  source_text_policy?: string;
  overread_risk?: string;
  summary?: string;
  confidence?: number;
};

export type CodeInvestigationTrace = {
  id: string;
  mode?: string;
  summary?: string;
  questions?: CodeInvestigationQuestion[];
  tool_calls?: CodeInvestigationToolCall[];
  total_files_discovered?: number;
  total_files_searched?: number;
  total_files_selected?: number;
  total_bytes_read?: number;
  created_at?: string;
  completed_at?: string;
};

export type CodeInvestigationQuestion = {
  id: string;
  question: string;
  intent_label?: string;
  expected_evidence?: string[];
  query_terms?: string[];
  status?: string;
  evidence_summary?: string;
  remaining_gaps?: string[];
  next_actions?: CodeInvestigationNextAction[];
  tool_call_ids?: string[];
  confidence?: number;
};

export type CodeInvestigationToolCall = {
  id: string;
  tool: string;
  purpose?: string;
  query?: string;
  input_summary?: string;
  output_summary?: string;
  selection_reason?: string;
  read_policy?: string;
  source_text_policy?: string;
  matched_file_count?: number;
  selected_file_count?: number;
  path_hashes?: string[];
  snippet_refs?: CodeSnippetRef[];
  evidence_refs?: EvidenceRef[];
  metadata?: Record<string, unknown>;
  confidence?: number;
  elapsed_ms?: number;
  fallback_reason?: string;
};

export type CodeSnippetRef = {
  id: string;
  path_hash_sha256: string;
  content_sha256?: string;
  line_start?: number;
  line_end?: number;
  matched_terms?: string[];
  signal_kinds?: string[];
  redacted_preview_sha256?: string;
};

export type RouteInsight = {
  id: string;
  path: string;
  name?: string;
  source_path_hash_sha256?: string;
  component_refs?: string[];
  auth_required?: boolean;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type ComponentInsight = {
  id: string;
  name: string;
  kind?: string;
  file_path_hash_sha256?: string;
  selector_hints?: string[];
  action_labels?: string[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type SelectorInsight = {
  kind: string;
  value: string;
  file_path_hash_sha256?: string;
  stability_score?: number;
  confidence?: number;
  evidence_refs?: EvidenceRef[];
};

export type APIEndpointInsight = {
  id: string;
  method?: string;
  path: string;
  file_path_hash_sha256?: string;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type DataModelInsight = {
  id: string;
  name: string;
  kind?: string;
  fields?: DataField[];
  source_path_hash_sha256?: string;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type SensitiveFieldFinding = {
  name: string;
  kind?: string;
  reason?: string;
  evidence_refs?: EvidenceRef[];
};

export type PageUnderstandingSnapshot = {
  id: string;
  project_id?: string;
  schema_version?: string;
  url?: string;
  title?: string;
  page_role?: string;
  screenshot_ref?: ArtifactRef;
  ocr_text?: string;
  vision_summary?: string;
  actions?: PageActionInsight[];
  stable_selectors?: SelectorCandidate[];
  states?: string[];
  risk_findings?: AgentFinding[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
  captured_at?: string;
  created_at?: string;
};

export type PageActionInsight = {
  id: string;
  label?: string;
  kind?: string;
  selector_hint?: string;
  target_url?: string;
  feature_ref?: string;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type MultimodalUnderstandingReport = {
  id: string;
  project_id: string;
  schema_version: "demoops.multimodal_understanding_report.v1";
  requirement_brief?: RequirementBrief;
  code_snapshots?: CodeUnderstandingSnapshot[];
  page_snapshots?: PageUnderstandingSnapshot[];
  summary?: string;
  feature_hypotheses?: Feature[];
  workflow_candidates?: WorkflowCandidate[];
  input_fingerprints?: Record<string, string>;
  source_digest_sha256?: string;
  evidence_refs?: EvidenceRef[];
  safety_report?: SafetyReport;
  confidence?: number;
  created_at?: string;
};

export type ProjectIntelligencePack = {
  id: string;
  project_id: string;
  schema_version: "demoops.project_intelligence_pack.v1" | string;
  demo_intent?: DemoIntentSpec;
  run_intent_scope?: RunIntentScope;
  architecture?: ProjectArchitectureMap;
  feature_capabilities?: FeatureCapability[];
  feature_trace?: FeatureTraceResult;
  interaction_surfaces?: InteractionSurface[];
  business_stage_plan?: BusinessStagePlan;
  verified_interaction_plan?: VerifiedInteractionPlan;
  missing_evidence_report?: MissingEvidenceReport;
  api_contracts?: APIContractSummary[];
  data_models?: ProjectDataModelSummary[];
  demo_scenario_plans?: DemoScenarioPlan[];
  script_readiness_report?: ScriptReadinessReport;
  safety_report?: SafetyReport;
  input_fingerprints?: Record<string, string>;
  source_digest_sha256?: string;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
  created_at?: string;
};

export type RunIntentScope = {
  id: string;
  project_id: string;
  schema_version?: string;
  product_origin?: string;
  product_url?: string;
  allowed_origins?: string[];
  forbidden_path_prefixes?: string[];
  forbidden_signals?: string[];
  created_at?: string;
};

export type DemoIntentSpec = {
  id: string;
  project_id: string;
  schema_version?: string;
  objective?: string;
  target_audience?: string;
  goals?: DemoIntentGoal[];
  forbidden_topics?: string[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
  created_at?: string;
};

export type DemoIntentGoal = {
  id: string;
  label: string;
  kind?: string;
  required: boolean;
  business_critical: boolean;
  target_keywords?: string[];
  preferred_action?: string;
  target_page_hint?: string;
  success_state?: string;
  forbidden?: boolean;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type FeatureTraceResult = {
  id: string;
  project_id: string;
  intent_id?: string;
  schema_version?: string;
  traces?: FeatureGoalTrace[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
  created_at?: string;
};

export type FeatureGoalTrace = {
  intent_goal_id: string;
  intent_label?: string;
  matched_route_refs?: string[];
  matched_components?: string[];
  matched_api_refs?: string[];
  matched_data_models?: string[];
  selector_evidence?: InteractionProbe[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
  missing_evidence?: string[];
};

export type BusinessStageKind =
  | "session_setup"
  | "business_action"
  | "business_input"
  | "mode_selection"
  | "business_submit"
  | "observe_progress"
  | "final_observe"
  | string;

export type BusinessRouteState =
  | "unauthenticated"
  | "workspace"
  | "creation_flow"
  | "project_detail"
  | "build_running"
  | string;

export type BusinessStagePlan = {
  id: string;
  project_id: string;
  intent_id?: string;
  schema_version?: string;
  stages: BusinessStage[];
  core_business_stage_count?: number;
  blocking_uncertainties?: StageUncertainty[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
  created_at?: string;
};

export type BusinessStage = {
  id: string;
  order: number;
  kind: BusinessStageKind;
  title?: string;
  objective?: string;
  user_intent?: string;
  route_state?: BusinessRouteState;
  entry_route?: string;
  expected_route_after_action?: string;
  duration_ms?: number;
  action: BusinessActionSemantics;
  targets?: BusinessTargetCandidate[];
  evidence_requirements?: EvidenceRequirement[];
  uncertainties?: StageUncertainty[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type BusinessActionSemantics = {
  type?: string;
  label?: string;
  input_semantic?: string;
  input_value?: string;
  input_ref?: string;
  secret_ref?: string;
  success_state?: string;
  wait_conditions?: string[];
  capture_points?: string[];
  parameters?: Record<string, string>;
  non_destructive?: boolean;
};

export type BusinessTargetCandidate = {
  id: string;
  intent_goal_id?: string;
  label?: string;
  kind?: string;
  selector?: string;
  url?: string;
  route_ref?: string;
  route?: string;
  component_ref?: string;
  role?: string;
  text?: string;
  test_id?: string;
  selector_score?: number;
  confidence?: number;
  is_verified?: boolean;
  verification_status?: string;
  verification_source?: string;
  evidence_refs?: EvidenceRef[];
  alternatives?: SelectorCandidate[];
};

export type EvidenceRequirement = {
  kind: string;
  required: boolean;
  satisfied: boolean;
  summary?: string;
  field_path?: string;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type InteractionProbe = {
  id: string;
  intent_goal_id?: string;
  label?: string;
  kind?: string;
  selector?: string;
  url?: string;
  route_ref?: string;
  component_ref?: string;
  source?: string;
  score?: number;
  is_business: boolean;
  is_chrome: boolean;
  selector_score?: number;
  wait_conditions?: string[];
  evidence_refs?: EvidenceRef[];
  alternatives?: SelectorCandidate[];
};

export type VerifiedInteractionPlan = {
  id: string;
  project_id: string;
  intent_id?: string;
  schema_version?: string;
  actions?: VerifiedInteractionAction[];
  business_action_count?: number;
  verification_mode?: string;
  browser_scan_id?: string;
  source_url?: string;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
  created_at?: string;
};

export type VerifiedInteractionAction = {
  id: string;
  intent_goal_id?: string;
  label?: string;
  kind?: string;
  selector?: string;
  url?: string;
  route_ref?: string;
  component_ref?: string;
  expected_outcome?: string;
  success_state?: string;
  input_value?: string;
  wait_conditions?: string[];
  duration_hint_ms?: number;
  is_business: boolean;
  verification_status: string;
  verification_source?: string;
  verified_at?: string;
  selector_score?: number;
  evidence_refs?: EvidenceRef[];
  alternatives?: SelectorCandidate[];
};

export type MissingEvidenceReport = {
  id: string;
  project_id: string;
  intent_id?: string;
  schema_version?: string;
  blocking: boolean;
  summary?: string;
  items?: MissingEvidenceItem[];
  evidence_refs?: EvidenceRef[];
  created_at?: string;
};

export type MissingEvidenceItem = {
  id: string;
  intent_goal_id?: string;
  intent_label?: string;
  missing_kind: string;
  severity: string;
  message: string;
  suggested_action?: string;
  field_path?: string;
  evidence_refs?: EvidenceRef[];
};

export type ProjectArchitectureMap = {
  id: string;
  project_id: string;
  schema_version?: string;
  repository_count?: number;
  repository_ref_ids?: string[];
  workspace_root_hash_sha256?: string;
  package_managers?: string[];
  frameworks?: string[];
  languages?: string[];
  runtime_targets?: string[];
  entrypoint_hashes?: string[];
  modules?: ProjectModule[];
  route_tree?: ArchitectureRouteNode[];
  summary?: string;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type ProjectModule = {
  id: string;
  name: string;
  kind?: string;
  responsibility?: string;
  repository_ref_id?: string;
  file_count?: number;
  source_path_hashes?: string[];
  entrypoint_hashes?: string[];
  route_refs?: string[];
  component_refs?: string[];
  api_refs?: string[];
  data_model_refs?: string[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type ArchitectureRouteNode = {
  id: string;
  path: string;
  name?: string;
  parent_path?: string;
  component_refs?: string[];
  auth_required?: boolean;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type FeatureCapability = {
  id: string;
  name: string;
  kind?: string;
  user_value?: string;
  business_value?: string;
  priority?: string;
  supporting_route_refs?: string[];
  supporting_page_refs?: string[];
  supporting_components?: string[];
  supporting_apis?: string[];
  supporting_data_models?: string[];
  key_actions?: string[];
  risks?: string[];
  evidence_refs?: EvidenceRef[];
  demo_value_score?: number;
  confidence?: number;
};

export type InteractionSurface = {
  id: string;
  page_id?: string;
  url?: string;
  title?: string;
  page_role?: string;
  actions?: UIActionRef[];
  stable_selectors?: SelectorCandidate[];
  states?: string[];
  wait_hints?: string[];
  feature_refs?: string[];
  risk_findings?: AgentFinding[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type UIActionRef = {
  id?: string;
  label?: string;
  kind?: string;
  selector?: string;
  target_route?: string;
  evidence_refs?: EvidenceRef[];
};

export type APIContractSummary = {
  id: string;
  method?: string;
  path: string;
  purpose?: string;
  auth_required?: boolean;
  request_fields?: string[];
  response_fields?: string[];
  sensitive_fields?: string[];
  file_path_hash_sha256?: string;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type ProjectDataModelSummary = {
  id: string;
  name: string;
  kind?: string;
  fields?: DataField[];
  sensitive_fields?: string[];
  source_path_hash_sha256?: string;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type DemoScenarioPlan = {
  id: string;
  name: string;
  use_case?: DemoUseCase;
  audience_id?: string;
  objective?: string;
  value_proposition?: string;
  feature_refs?: string[];
  page_refs?: string[];
  route_refs?: string[];
  estimated_steps?: number;
  estimated_duration_sec?: number;
  narrative_beats?: string[];
  risk_notes?: string[];
  evidence_refs?: EvidenceRef[];
  feasibility?: number;
  value_score?: number;
  confidence?: number;
};

export type ScriptReadinessReport = {
  id: string;
  project_id: string;
  schema_version: "demoops.script_readiness_report.v1" | string;
  can_proceed: boolean;
  summary?: string;
  blockers?: AgentFinding[];
  warnings?: AgentFinding[];
  missing_inputs?: string[];
  repair_suggestions?: string[];
  recommended_scenario_id?: string;
  recommended_scenario_name?: string;
  suggested_stage_count?: number;
  suggested_target_duration_sec?: number;
  selector_coverage?: number;
  business_action_count?: number;
  generic_selector_count?: number;
  login_action_count?: number;
  login_duplication?: boolean;
  min_stage_duration_ms?: number;
  blocking_assertion_risk_count?: number;
  code_investigation_tool_driven?: boolean;
  code_investigation_overread_risk?: string;
  code_investigation_specialized_tool_call_count?: number;
  code_investigation_open_question_count?: number;
  code_investigation_summary?: string;
  code_investigation_gaps?: string[];
  credential_coverage?: boolean;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
  created_at?: string;
};

export type AgentGraphTrace = {
  id: string;
  project_id: string;
  schema_version: "demoops.agent_graph_trace.v1" | string;
  graph_name?: string;
  steps?: AgentGraphTraceStep[];
  summary?: string;
  fallback_reason?: string;
  started_at?: string;
  completed_at?: string;
};

export type AgentGraphTraceStep = {
  id: string;
  node_id: string;
  agent?: string;
  tool?: string;
  status: string;
  input_summary?: string;
  output_summary?: string;
  elapsed_ms?: number;
  confidence?: number;
  fallback_reason?: string;
  evidence_refs?: EvidenceRef[];
  started_at?: string;
  completed_at?: string;
};

export type AgentFinding = {
  id: string;
  kind: string;
  severity: "info" | "warning" | "blocking";
  title?: string;
  summary: string;
  rationale?: string;
  suggested_action?: string;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type SafetyReport = {
  allowed_to_proceed: boolean;
  policy_findings?: AgentFinding[];
  masked_fields?: string[];
  notes?: string[];
};

export type RecordingRunSpec = {
  run_id?: string;
  base_url: string;
  allowed_domains: string[];
  required_ip_allowlist?: string[];
  auth_flow_ref?: string;
  timezone?: string;
  locale?: string;
  browser: BrowserRunSpec;
  timeline: RecordingTimeline;
  outputs: RecordingOutputRequest;
  redactions: RedactionPolicy;
  failure_policy: RecordingFailurePolicy;
  sandbox_policy?: SandboxPolicy;
  environment?: Record<string, string>;
};

export type SandboxPolicy = {
  profile?: "dev" | "mvp_cloud" | "enterprise" | string;
  isolation_mode?: "local_sidecar" | "per_job_container" | "per_job_microvm" | string;
  network_policy: SandboxNetworkPolicy;
  filesystem_policy: SandboxFilesystemPolicy;
  resource_limits: SandboxResourceLimits;
  browser_policy: SandboxBrowserPolicy;
  secret_policy: SandboxSecretPolicy;
  artifact_policy: SandboxArtifactPolicy;
  diagnostic_policy: SandboxDiagnosticPolicy;
  policy_hash_sha256?: string;
};

export type SandboxNetworkPolicy = {
  mode?: "deny_all" | "allowed_domains_only" | "no_customer_network" | string;
  allowed_domains?: string[];
  allowed_cascade_endpoints?: string[];
  denied_cidrs?: string[];
  proxy_required: boolean;
  dns_policy?: string;
};

export type SandboxFilesystemPolicy = {
  mode?: "tmpfs_workspace" | "read_only_root" | string;
  writable_paths?: string[];
  no_host_mount: boolean;
  no_docker_socket: boolean;
  delete_temp_after_run: boolean;
};

export type SandboxResourceLimits = {
  max_runtime_sec?: number;
  max_memory_mb?: number;
  max_cpu_count?: number;
  max_disk_mb?: number;
};

export type SandboxBrowserPolicy = {
  fresh_context_per_run: boolean;
  disable_extensions: boolean;
  disable_downloads: boolean;
  trace_sources: boolean;
  allowed_page_methods?: string[];
  allowed_context_apis?: string[];
};

export type SandboxSecretPolicy = {
  vault_only: boolean;
  allowed_secret_refs?: string[];
  inject_via_context_only: boolean;
  forbid_env_injection: boolean;
  revoke_after_run: boolean;
  rotation_required_after_run: boolean;
};

export type SandboxArtifactPolicy = {
  encrypt_sensitive_artifacts: boolean;
  sensitive_by_default: boolean;
  recipient_kind?: string;
  recipient_key_id?: string;
  retention?: Record<string, unknown>;
  require_checksum: boolean;
};

export type SandboxDiagnosticPolicy = {
  redaction_required: boolean;
  forbid_full_html: boolean;
  strip_headers?: string[];
  strip_storage_keys?: string[];
  encrypt_diagnostics: boolean;
  return_repair_hints: boolean;
};

export type SandboxExecutionMetadata = {
  policy_hash_sha256?: string;
  profile?: string;
  isolation_mode?: string;
  network_mode?: string;
  worker_id?: string;
  container_id?: string;
  micro_vm_id?: string;
  runtime_versions?: Record<string, string>;
};

export type BrowserRunSpec = {
  engine: string;
  version_policy?: string;
  headless: boolean;
  viewports?: ViewportSpec[];
};

export type RecordingTimeline = {
  target_duration_sec: number;
  max_duration_sec?: number;
  capture_windows?: CaptureWindow[];
  node_timing_hints?: NodeTimingHint[];
};

export type CaptureWindow = {
  id: string;
  node_id?: string;
  start_ms: number;
  duration_ms: number;
  role?: string;
};

export type NodeTimingHint = {
  node_id: string;
  duration_ms?: number;
  hold_after_ms?: number;
};

export type RecordingOutputRequest = {
  raw_recording: boolean;
  final_video: boolean;
  screenshot_pack: boolean;
  step_by_step_docs: boolean;
  trace: boolean;
  output_formats?: string[];
  resolution_width?: number;
  resolution_height?: number;
};

export type RedactionPolicy = {
  mask_selectors?: string[];
  text_patterns?: string[];
  video_mask_policy?: string;
  screenshot_mask_policy?: string;
  redactions?: RedactionSpec[];
};

export type RecordingFailurePolicy = {
  retry_attempts?: number;
  selector_repair_allowed: boolean;
  data_repair_allowed: boolean;
  max_repair_attempts?: number;
  human_escalation_conditions?: string[];
};

export type ReproducibilitySpec = {
  graph_hash_sha256: string;
  package_hash_sha256?: string;
  script_hash_sha256?: string;
  input_fingerprints?: Record<string, string>;
  browser_runtime_pins?: Record<string, string>;
  source_snapshot_digest?: string;
  deterministic_seed?: string;
  created_with_app_version?: string;
};

export type PackageArtifactDescriptor = {
  id: string;
  role?: string;
  kind: string;
  uri: string;
  mime_type?: string;
  sha256: string;
  size_bytes?: number;
  encrypted: boolean;
  sensitive?: boolean;
  compression_alg?: string;
  recipient_key_id?: string;
  metadata?: Record<string, unknown>;
};

export type ExchangeProducer = {
  app_version?: string;
  install_id?: string;
  device_id?: string;
  os?: string;
  arch?: string;
  runtime_profile?: string;
};

export type ExchangeCrypto = {
  crypto_suite?: "aes-256-gcm" | "xchacha20-poly1305" | string;
  key_wrapping_mode?: "server_kms" | "server_public_key" | "customer_kms" | string;
  server_key_id: string;
  key_encryption_alg?: string;
  content_encryption_alg: string;
  compression_alg?: "gzip" | "none" | string;
  payload_digest_alg?: "sha256" | string;
  payload_digest_sha256: string;
  ciphertext_digest_sha256?: string;
  signature_alg: string;
  signature_key_id: string;
  signature: string;
  nonce: string;
  encrypted_content_key?: string;
  content_key_ref?: string;
  kms_key_ref?: string;
};

export type EncryptedPayloadRef = {
  kind: "inline" | "artifact" | string;
  inline_ciphertext?: string;
  artifact_id?: string;
  uri?: string;
  mime_type?: string;
  sha256?: string;
  size_bytes?: number;
  encrypted?: boolean;
  sensitive?: boolean;
  compression_alg?: string;
  dev_plaintext?: boolean;
};

export type ExchangeEnvelope = {
  envelope_id: string;
  org_id: string;
  project_id: string;
  package_kind: "client_execution" | string;
  schema_version: "demoops.exchange_envelope.v1";
  payload_schema_version: "demoops.client_execution_package.v1" | string;
  idempotency_key: string;
  created_at: string;
  expires_at: string;
  producer: ExchangeProducer;
  crypto: ExchangeCrypto;
  payload_ref: EncryptedPayloadRef;
  attachments?: PackageArtifactDescriptor[];
  policy: {
    retention?: Record<string, unknown>;
    data_residency?: string;
    replay_protection: boolean;
    max_execution_window_sec?: number;
    delete_payload_after_run: boolean;
    allow_delta_package?: boolean;
    human_approval_required: boolean;
    structure_summary_only: boolean;
    required_ip_allowlist_ack: boolean;
  };
};

export type ExecutionPackageInitRequest = {
  org_id: string;
  project_id: string;
  package_kind: "client_execution";
  producer?: ExchangeProducer;
};

export type ExecutionPackageInitResponse = {
  upload_id: string;
  server_public_key_id: string;
  server_public_key_alg?: string;
  server_public_keys?: Array<{
    key_id: string;
    alg: string;
    public_key: string;
    not_before?: string;
    expires_at?: string;
  }>;
  key_wrapping_modes?: string[];
  supported_crypto_suites?: string[];
  supported_compression?: string[];
  required_signature_alg?: string;
  installation_required?: boolean;
  session_expires_at?: string;
  result_recipient_key_id?: string;
  cascade_execution_ips: string[];
  max_envelope_bytes: number;
  max_attachment_bytes: number;
  expires_at: string;
};

export type ExecutionPackageUploadRequest = {
  upload_id: string;
  envelope: ExchangeEnvelope;
  payload_ref?: EncryptedPayloadRef;
};

export type ExecutionPackageUploadBody = ExecutionPackageUploadRequest & {
  payload?: ClientExecutionPackage;
};

export type ResultPackageAckRequest = {
  result_package_id: string;
  acked_by_install_id?: string;
  received_asset_ids?: string[];
  verified_checksums?: boolean;
  checksum_mismatch_ids?: string[];
  acked_at?: string;
};

export type ClientExecutionPackage = {
  package_id: string;
  org_id: string;
  project_id: string;
  schema_version: "demoops.client_execution_package.v1";
  created_at?: string;
  approved_at?: string;
  project_context_summary?: Record<string, unknown>;
  product_map_summary?: Record<string, unknown>;
  workflow_graph: DemoWorkflowGraph;
  recording_run_spec: RecordingRunSpec;
  executable_script_bundle: ExecutableRecordingScriptBundle;
  credential_grants?: Record<string, unknown>[];
  evidence_bundle?: Record<string, unknown>;
  reproducibility: ReproducibilitySpec;
  safety_report?: Record<string, unknown>;
  repair_context?: ScriptRepairContext;
  metadata?: Record<string, unknown>;
};

export type AgentError = {
  code: string;
  message: string;
  retryable?: boolean;
  evidence_refs?: EvidenceRef[];
};

export type ConsoleEventSummary = {
  level: string;
  message: string;
  url?: string;
  timestamp?: string;
};

export type NetworkEventSummary = {
  url: string;
  method?: string;
  status?: number;
  failure?: string;
  resource?: string;
  timestamp?: string;
  redacted?: boolean;
};

export type DiagnosticRedactionReport = {
  applied: boolean;
  policy_ref?: string;
  masked_selectors?: string[];
  masked_text_patterns?: string[];
  stripped_headers?: string[];
  stripped_storage_keys?: string[];
  full_html_included: boolean;
  policy_findings?: AgentFinding[];
};

export type ScriptRepairHint = {
  kind: string;
  summary: string;
  node_id?: string;
  selector_candidates?: SelectorCandidate[];
  suggested_action?: string;
  confidence?: number;
  evidence_refs?: EvidenceRef[];
};

export type ScriptFailureDiagnostic = {
  id: string;
  schema_version: "demoops.script_failure_diagnostic.v1";
  source_package_id: string;
  cloud_job_id: string;
  failed_node_id: string;
  failed_step_order?: number;
  attempt?: number;
  error: AgentError;
  current_url?: string;
  page_title?: string;
  screenshot_refs?: PackageArtifactDescriptor[];
  trace_refs?: PackageArtifactDescriptor[];
  console_events?: ConsoleEventSummary[];
  network_events?: NetworkEventSummary[];
  dom_snapshot_ref?: PackageArtifactDescriptor;
  accessibility_snapshot_ref?: PackageArtifactDescriptor;
  redaction_report: DiagnosticRedactionReport;
  repair_hints?: ScriptRepairHint[];
  captured_at?: string;
};

export type ScriptRepairRequest = {
  id: string;
  source_result_id: string;
  source_package_id: string;
  cloud_job_id: string;
  failed_bundle_hash_sha256?: string;
  failed_plan_hash_sha256?: string;
  max_repair_attempts?: number;
  repair_attempt?: number;
  approval_required: boolean;
  requested_at?: string;
  expires_at?: string;
};

export type ScriptRepairContext = {
  source_result_id: string;
  source_package_id: string;
  source_cloud_job_id: string;
  repair_attempt: number;
  base_bundle_id?: string;
  base_bundle_hash_sha256?: string;
  base_plan_hash_sha256?: string;
  diagnostic_refs?: EvidenceRef[];
  failure_diagnostic?: ScriptFailureDiagnostic;
  user_approval?: UserApprovalRecord;
  idempotency_key?: string;
};

export type ScriptRepairLineage = {
  base_bundle_id: string;
  base_bundle_hash_sha256: string;
  source_result_id: string;
  source_cloud_job_id: string;
  repair_attempt: number;
  change_summary?: string;
  diagnostic_refs?: EvidenceRef[];
  created_at?: string;
};

export type UserApprovalRecord = {
  approval_id: string;
  approved_by_user_id?: string;
  approved_at: string;
  plan_digest_sha256: string;
  reviewed_node_ids?: string[];
  notes?: string[];
};

export type StepResult = {
  node_id: string;
  status: string;
  started_at?: string;
  completed_at?: string;
  duration_ms?: number;
  observed_state?: string;
  validation_ids?: string[];
  artifacts?: ArtifactRef[];
  error?: AgentError;
};

export type ExecutionTrace = {
  id: string;
  workflow_graph_id: string;
  graph_version: number;
  started_at?: string;
  completed_at?: string;
  pass_rate?: number;
  step_results?: StepResult[];
  artifacts?: ArtifactRef[];
  environment?: Record<string, string>;
  sandbox?: SandboxExecutionMetadata;
};

export type VerificationReport = {
  pass_rate?: number;
  failed_node_ids?: string[];
  policy_findings?: AgentFinding[];
  reproducibility_match: boolean;
  output_checksums?: Array<{ id?: string; kind?: string; sha256: string; size_bytes?: number }>;
};

export type CloudExecutionAuditTrail = {
  cloud_worker_id?: string;
  started_at?: string;
  completed_at?: string;
  runtime_versions?: Record<string, string>;
  sandbox?: SandboxExecutionMetadata;
  source_package_digest?: string;
  graph_digest?: string;
  execution_ip?: string;
};

export type RecordingResultPackage = {
  result_id: string;
  source_package_id: string;
  cloud_job_id: string;
  schema_version: "demoops.recording_result_package.v1";
  status: "generated" | "delivered" | "acked" | "failed";
  execution_trace?: ExecutionTrace;
  step_results?: StepResult[];
  generated_assets?: ArtifactRef[];
  verification_report: VerificationReport;
	 execution_runtime?: string;
	 validation_reports?: RuntimeValidationReport[];
	 patch_ledger?: RuntimePatchLedgerEntry[];
	 stage_event_log_ref?: ArtifactRef;
  failure_diagnostic?: ScriptFailureDiagnostic;
  repair_request?: ScriptRepairRequest;
  graph_patch_suggestions?: unknown[];
  audit_trail?: CloudExecutionAuditTrail;
  delivery?: {
    result_package_ref: PackageArtifactDescriptor;
    asset_refs?: PackageArtifactDescriptor[];
    recipient_kind?: string;
    recipient_key_id?: string;
    encryption_alg?: string;
    expires_at?: string;
    ack_required: boolean;
    acked_at?: string;
  };
  created_at: string;
};

export type RuntimeValidationReport = {
	 schema_version: "demoops.validation_report.v1";
	 report_id: string;
	 run_id: string;
	 source_package_id: string;
	 source_bundle_hash_sha256: string;
	 policy_hash_sha256: string;
	 phase: "pre_execution" | "runtime_stage" | "post_execution";
	 node_id?: string;
	 stage_id?: string;
	 decision: "continue" | "repair_allowed" | "stop_and_report" | "reunderstanding_required";
	 pass_rate: number;
	 overall_confidence: number;
	 evidence_quality: "actual_browser_observation" | "browser_assertion" | "artifact_observation" | "derived_from_plan" | "insufficient_evidence";
	 evidence_refs?: EvidenceRef[];
	 repair_proposal_refs?: string[];
	 created_at: string;
};

export type RuntimePatchLedgerEntry = {
	 schema_version: "demoops.patch_ledger_entry.v1";
	 entry_id: string;
	 proposal_id: string;
	 run_id: string;
	 node_id: string;
	 stage_id: string;
	 attempt: number;
	 source_bundle_hash_sha256: string;
	 policy_hash_sha256: string;
	 field: string;
	 before?: string;
	 after: string;
	 policy_decision: "repair_allowed" | "stop_and_report";
	 applied: boolean;
	 rolled_back?: boolean;
	 evidence_refs?: EvidenceRef[];
	 applied_at?: string;
};

export type ExecutionScriptDocument = {
  id: string;
  project_id: string;
  workflow_graph_id: string;
  graph_version: number;
  schema_version: "demoops.execution_script_document.v1";
  status?: "draft" | "review_ready" | "approved";
  title?: string;
  summary?: string;
  language?: string;
  workflow_graph?: DemoWorkflowGraph;
  recording_run_spec: RecordingRunSpec;
  steps: ScriptStep[];
  safety_policy: ScriptSafetyPolicy;
  reproducibility: ReproducibilitySpec;
  approval_checklist: ScriptApprovalChecklist;
  evidence_refs?: EvidenceRef[];
  markdown_artifact?: ArtifactRef;
  created_at?: string;
  updated_at?: string;
};

export type ScriptStep = {
  id: string;
  order: number;
  node_id: string;
  title?: string;
  business_value?: string;
  page_target: ScriptPageTarget;
  action: ScriptActionInstruction;
  target_contract?: BrowserAgentTargetContract;
  expected_outcome: string;
  validations: ValidationSpec[];
  capture: CaptureSpec;
  timing: NodeTimingHint;
  narrative: NarrativeCue;
  evidence_refs?: EvidenceRef[];
  blocking: boolean;
};

export type ScriptPageTarget = {
  url?: string;
  selector?: string;
  selector_alternatives?: SelectorCandidate[];
  page_ref?: string;
};

export type ScriptActionInstruction = {
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

export type ScriptSafetyPolicy = {
  allowed_domains?: string[];
  forbidden_pages?: string[];
  forbidden_data?: string[];
  redactions: RedactionPolicy;
  pii_handling?: string;
};

export type ScriptApprovalChecklist = {
  human_approval_required: boolean;
  source_summary_only: boolean;
  credential_scope_review_required: boolean;
  redactions_review_required: boolean;
  ip_allowlist_acknowledgement_required: boolean;
  blocking_reasons?: string[];
};

export type ExecutableScriptBundleStatus = "draft" | "review_ready" | "validated" | "rejected";

export type ExecutableRecordingScriptBundle = {
  id: string;
  project_id: string;
  workflow_graph_id: string;
  schema_version: "demoops.executable_recording_script_bundle.v1";
  status?: ExecutableScriptBundleStatus;
  script_manifest: ExecutableScriptManifest;
  plan_json: ExecutionScriptDocument;
  playwright_script: ExecutableScriptSource;
  stage_approval_plan?: StageApprovalPlan;
  script_outline?: BrowserAgentScriptOutline;
  agent_prompt_policy?: BrowserAgentPromptPolicy;
  browser_agent_contract?: BrowserAgentContract;
  understanding_dossier_ref?: ArtifactRef;
  project_understanding_dossier?: ProjectUnderstandingDossier;
  approval_markdown: ApprovalMarkdownDocument;
  security_policy: ExecutableScriptSecurityPolicy;
  reproducibility: ExecutableScriptReproducibility;
  validation?: ExecutableScriptValidation;
  repair_lineage?: ScriptRepairLineage;
  created_at?: string;
  updated_at?: string;
};

export type ExecutableScriptManifest = {
  script_id: string;
  version: number;
  language: string;
  runtime: string;
  entry_function: string;
  generator: string;
  generator_version: string;
  dependency_allowlist?: string[];
  context_apis?: string[];
  step_node_ids: string[];
};

export type ExecutableScriptSource = {
  inline_source?: string;
  artifact?: ArtifactRef;
  mime_type?: string;
  sha256: string;
  size_bytes?: number;
  encrypted?: boolean;
};

export type ApprovalMarkdownDocument = {
  inline_markdown?: string;
  artifact?: ArtifactRef;
  mime_type?: string;
  sha256: string;
  size_bytes?: number;
};

export type ExecutableScriptSecurityPolicy = {
  allowed_domains?: string[];
  forbidden_pages?: string[];
  forbidden_data?: string[];
  redactions: RedactionPolicy;
  secret_refs?: string[];
  allowed_context_apis?: string[];
  allowed_page_methods?: string[];
  forbidden_imports?: string[];
  forbidden_identifiers?: string[];
  network_policy?: string;
  file_system_policy?: string;
};

export type ExecutableScriptReproducibility = {
  plan_hash_sha256: string;
  script_hash_sha256?: string;
  stage_plan_hash_sha256?: string;
  outline_hash_sha256?: string;
  prompt_policy_hash_sha256?: string;
  understanding_dossier_hash_sha256?: string;
  browser_agent_contract_hash_sha256?: string;
  markdown_hash_sha256: string;
  bundle_hash_sha256?: string;
  graph_hash_sha256?: string;
  source_snapshot_digest?: string;
  generator_version?: string;
  deterministic_seed?: string;
  input_fingerprints?: Record<string, string>;
};

export type StageApprovalPlan = {
  id: string;
  project_id: string;
  workflow_graph_id: string;
  schema_version: "demoops.stage_approval_plan.v1" | string;
  title?: string;
  summary?: string;
  runtime?: string;
  language?: string;
  stages: StageApprovalStage[];
  safety_policy: ScriptSafetyPolicy;
  uncertainty_report?: StageUncertainty[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
  created_at?: string;
  updated_at?: string;
};

export type StageApprovalStage = {
  id: string;
  order: number;
  node_id: string;
  business_stage_id?: string;
  stage_kind?: BusinessStageKind;
  route_state?: BusinessRouteState;
  title?: string;
  objective?: string;
  business_intent?: string;
  duration_ms?: number;
  entry_route?: string;
  target_route?: string;
  target_route_template?: string;
  expected_route_after_action?: string;
  runtime_route_verification_required?: boolean;
  candidate_routes?: BrowserAgentRouteCandidate[];
  target_url?: string;
  component_refs?: string[];
  api_refs?: string[];
  style_refs?: string[];
  data_model_refs?: string[];
  input_content?: StageInputContent[];
  interaction: BrowserAgentInteraction;
  target_contract?: BrowserAgentTargetContract;
  success_state?: string;
  wait_conditions?: string[];
  capture_points?: string[];
  capture_plan?: BrowserAgentCapturePlan;
  risk_notes?: string[];
  investigation_question_refs?: InvestigationQuestionRef[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type StageInputContent = {
  kind?: string;
  label?: string;
  value?: string;
  input_ref?: string;
  secret_ref?: string;
  editable?: boolean;
  evidence_refs?: EvidenceRef[];
};

export type BrowserAgentCapturePlan = {
  intent?: string;
  shot_type?: string;
  primary_artifact?: string;
  required_assets?: string[];
  min_duration_ms?: number;
  pre_capture_wait_ms?: number;
  hold_after_ms?: number;
  clip_suggestion?: string;
  notes?: string[];
};

export type BrowserAgentScriptOutline = {
  id: string;
  project_id: string;
  workflow_graph_id: string;
  schema_version: "demoops.browser_agent_script_outline.v1" | string;
  runtime: string;
  base_url?: string;
  product_origin?: string;
  summary?: string;
  stages: BrowserAgentOutlineStage[];
  allowed_exploration_scope: BrowserAgentExplorationScope;
  forbidden_actions?: string[];
  server_editable_fields?: string[];
  immutable_fields?: string[];
  uncertainty_report?: StageUncertainty[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
  created_at?: string;
  updated_at?: string;
};

export type BrowserAgentExplorationScope = {
  allowed_origins?: string[];
  allowed_routes?: string[];
  forbidden_path_prefixes?: string[];
  forbidden_keywords?: string[];
  max_depth?: number;
  allow_non_destructive: boolean;
};

export type BrowserAgentRouteCandidate = {
  route: string;
  route_id?: string;
  name?: string;
  source?: string;
  matched_keyword?: string;
  auth_required?: boolean;
  runtime_dynamic?: boolean;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type BrowserAgentOutlineStage = {
  id: string;
  stage_id: string;
  order: number;
  node_id: string;
  business_stage_id?: string;
  stage_kind?: BusinessStageKind;
  route_state?: BusinessRouteState;
  objective?: string;
  entry_route?: string;
  route?: string;
  target_route_template?: string;
  expected_route_after_action?: string;
  runtime_route_verification_required?: boolean;
  candidate_routes?: BrowserAgentRouteCandidate[];
  url?: string;
  components?: BrowserAgentComponentTarget[];
  interactions?: BrowserAgentInteraction[];
  target_contract?: BrowserAgentTargetContract;
  wait_conditions?: string[];
  capture_points?: string[];
  capture_plan?: BrowserAgentCapturePlan;
  success_state?: string;
  duration_ms?: number;
  can_modify?: string[];
  must_preserve?: string[];
  investigation_question_refs?: InvestigationQuestionRef[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type InvestigationQuestionRef = {
  id: string;
  intent_label?: string;
  status?: string;
  evidence_summary?: string;
  remaining_gaps?: string[];
  next_actions?: CodeInvestigationNextAction[];
  tool_call_ids?: string[];
  confidence?: number;
};

export type CodeInvestigationNextAction = {
  tool: string;
  reason?: string;
  query_terms?: string[];
  command_kind?: string;
  expected_evidence?: string[];
  depends_on_tool_call_id?: string;
};

export type BrowserAgentTargetContract = {
  semantic_id: string;
  purpose?: string;
  allowed_roles?: string[];
  allowed_names?: string[];
  forbidden_names?: string[];
  component_ref?: string;
  destructive: boolean;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type BrowserAgentComponentTarget = {
  component_ref?: string;
  route_ref?: string;
  role?: string;
  name?: string;
  text?: string;
  label?: string;
  test_id?: string;
  selector?: string;
  selector_alternatives?: SelectorCandidate[];
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type BrowserAgentInteraction = {
  kind: GraphActionType;
  target?: ActionTarget;
  value?: string;
  input_ref?: string;
  secret_ref?: string;
  parameters?: Record<string, unknown>;
  wait_until?: string;
  wait_conditions?: string[];
  non_destructive?: boolean;
  selector_policy?: string;
  evidence_refs?: EvidenceRef[];
};

export type BrowserAgentPromptPolicy = {
  id: string;
  project_id: string;
  workflow_graph_id: string;
  schema_version: "demoops.browser_agent_prompt_policy.v1" | string;
  runtime: string;
  system_prompt: string;
  immutable_fields: string[];
  editable_fields: string[];
  forbidden_changes: string[];
  repair_policy?: string[];
  evidence_policy?: string[];
  safety_boundaries?: string[];
  human_review_required: boolean;
  created_at?: string;
  updated_at?: string;
};

export type BrowserAgentContract = {
  id: string;
  project_id: string;
  workflow_graph_id: string;
  schema_version: "demoops.browser_agent_contract.v1" | string;
  mode: string;
  business_authority: BrowserAgentBusinessAuthority;
  repair_policy: BrowserAgentRepairPolicy;
  observation_policy: BrowserAgentObservationPolicy;
  model_policy: BrowserAgentModelPolicy;
  conflict_policy: BrowserAgentConflictPolicy;
  created_at?: string;
  updated_at?: string;
};

export type BrowserAgentBusinessAuthority = {
  source: string;
  script_may_change_business_intent: boolean;
  runtime_page_may_change_business_intent: boolean;
  agent_may_change_business_intent: boolean;
};

export type BrowserAgentRepairPolicy = {
  allowed_repair_kinds?: string[];
  editable_fields?: string[];
  immutable_fields?: string[];
  max_repair_attempts?: number;
  max_patch_operations?: number;
  max_agent_runtime_ms?: number;
  min_auto_apply_confidence?: number;
  same_node_only: boolean;
  same_action_type_only: boolean;
  same_domain_only: boolean;
  step_insert_allowed: boolean;
  step_delete_allowed: boolean;
  step_reorder_allowed: boolean;
  step_skip_allowed: boolean;
};

export type BrowserAgentObservationPolicy = {
  allowed_fields?: string[];
  full_html_allowed: boolean;
  cookies_allowed: boolean;
  authorization_headers_allowed: boolean;
  storage_content_allowed: boolean;
  input_values_allowed: boolean;
  source_code_allowed: boolean;
  max_interactive_elements?: number;
  max_text_length?: number;
  mask_selectors?: string[];
};

export type BrowserAgentModelPolicy = {
  provider_ref?: string;
  deployment_mode?: string;
  customer_data_export_allowed: boolean;
  training_usage_allowed: boolean;
  retention_allowed: boolean;
  data_residency?: string;
  request_timeout_ms?: number;
};

export type BrowserAgentConflictPolicy = {
  on_graph_plan_conflict?: string;
  on_target_contract_conflict?: string;
  on_runtime_intent_conflict?: string;
  on_ambiguous_target?: string;
  on_outcome_verification_failure?: string;
  on_repair_limit_exceeded?: string;
};

export type ProjectUnderstandingDossier = {
  id: string;
  project_id: string;
  workflow_graph_id?: string;
  schema_version: "demoops.project_understanding_dossier.v1" | string;
  summary?: string;
  requirement_objective?: string;
  architecture_summary?: string;
  route_evidence?: DossierEvidence[];
  component_evidence?: DossierEvidence[];
  style_evidence?: DossierEvidence[];
  api_evidence?: DossierEvidence[];
  data_model_evidence?: DossierEvidence[];
  interaction_evidence?: DossierEvidence[];
  security_evidence?: DossierEvidence[];
  uncertainty_report?: StageUncertainty[];
  source_digest_sha256?: string;
  input_fingerprints?: Record<string, string>;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
  created_at?: string;
  updated_at?: string;
};

export type DossierEvidence = {
  id: string;
  kind?: string;
  label?: string;
  summary?: string;
  route?: string;
  component_ref?: string;
  file_path_hash_sha256?: string;
  source_path_hash_sha256?: string;
  evidence_refs?: EvidenceRef[];
  confidence?: number;
};

export type StageUncertainty = {
  id: string;
  stage_id?: string;
  node_id?: string;
  kind?: string;
  summary?: string;
  blocking?: boolean;
  suggested_action?: string;
  evidence_refs?: EvidenceRef[];
};

export type ExecutableScriptValidation = {
  valid: boolean;
  findings?: AgentFinding[];
  validated_at?: string;
};
