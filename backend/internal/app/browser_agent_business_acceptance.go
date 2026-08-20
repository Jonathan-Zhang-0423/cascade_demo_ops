package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

// BrowserAgentBusinessAcceptanceReport proves a real, but Server-owned,
// business flow. It is deliberately separate from the fixed safety gate: the
// latter protects the protocol boundary; this one proves input, state change,
// result verification and the editor handoff work together.
type BrowserAgentBusinessAcceptanceReport struct {
	SchemaVersion         string                           `json:"schema_version"`
	GeneratedAt           time.Time                        `json:"generated_at"`
	Runtime               string                           `json:"runtime"`
	StrictGate            string                           `json:"strict_gate"`
	PackageID             string                           `json:"package_id"`
	BusinessFlow          string                           `json:"business_flow"`
	Stages                []BrowserAgentAcceptanceScenario `json:"stages"`
	EditorMaterialization EditorSessionMaterialization     `json:"editor_materialization"`
}

type BrowserAgentBusinessAcceptanceView struct {
	Ready      bool                                  `json:"ready"`
	CanRun     bool                                  `json:"can_run"`
	Message    string                                `json:"message"`
	ReportPath string                                `json:"report_path,omitempty"`
	Report     *BrowserAgentBusinessAcceptanceReport `json:"report,omitempty"`
}

func (s *Service) GetBrowserAgentBusinessAcceptance(ctx context.Context) (BrowserAgentBusinessAcceptanceView, error) {
	_ = ctx
	view := BrowserAgentBusinessAcceptanceView{
		CanRun:  s.browserAgentAcceptanceFixturePath() != "" && fileExists(s.localVideoWorkerPath()) && commandReady(s.nodeBinaryForExecution()),
		Message: "尚未运行受控业务验收包。该验收只访问 Server 临时启动的演示业务页面，不访问用户产品、App 数据包或生产凭据。",
	}
	report, err := readBrowserAgentBusinessAcceptanceReport(s.browserAgentBusinessAcceptanceReportPath())
	if errors.Is(err, os.ErrNotExist) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	view.Ready, view.ReportPath, view.Report = true, s.browserAgentBusinessAcceptanceReportPath(), &report
	if report.StrictGate == "passed" {
		view.Message = "最近一次受控业务验收已通过：项目名输入、模式选择、提交、结果页验证、录屏追踪和编辑器素材交接均已留存证据。"
	} else {
		view.Message = "最近一次受控业务验收未通过；应先修复失败阶段，不能把它当作真实 App 执行能力。"
	}
	return view, nil
}

func (s *Service) RunBrowserAgentBusinessAcceptance(ctx context.Context) (BrowserAgentBusinessAcceptanceView, error) {
	if s == nil {
		return BrowserAgentBusinessAcceptanceView{}, errors.New("browser-agent business acceptance service is not configured")
	}
	fixturePath := s.browserAgentAcceptanceFixturePath()
	if fixturePath == "" || !fileExists(fixturePath) {
		return BrowserAgentBusinessAcceptanceView{}, errors.New("browser-agent outline fixture is not available")
	}
	if !fileExists(s.localVideoWorkerPath()) {
		return BrowserAgentBusinessAcceptanceView{}, errors.New("video-worker build artifact is not available")
	}
	if err := checkCommandReady(s.nodeBinaryForExecution()); err != nil {
		return BrowserAgentBusinessAcceptanceView{}, err
	}
	if err := os.MkdirAll(filepath.Dir(s.browserAgentBusinessAcceptanceReportPath()), 0o700); err != nil {
		return BrowserAgentBusinessAcceptanceView{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	report, err := s.runBrowserAgentBusinessAcceptance(runCtx, fixturePath)
	if err != nil {
		return BrowserAgentBusinessAcceptanceView{}, err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return BrowserAgentBusinessAcceptanceView{}, err
	}
	if err := os.WriteFile(s.browserAgentBusinessAcceptanceReportPath(), append(data, '\n'), 0o600); err != nil {
		return BrowserAgentBusinessAcceptanceView{}, err
	}
	return s.GetBrowserAgentBusinessAcceptance(ctx)
}

func (s *Service) browserAgentBusinessAcceptanceReportPath() string {
	if s == nil || s.runtime.ArtifactRoot == "" {
		return ""
	}
	return filepath.Join(s.runtime.ArtifactRoot, "browser-agent-business-acceptance", "latest", "acceptance-report.json")
}

func readBrowserAgentBusinessAcceptanceReport(path string) (BrowserAgentBusinessAcceptanceReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BrowserAgentBusinessAcceptanceReport{}, err
	}
	var report BrowserAgentBusinessAcceptanceReport
	if err := json.Unmarshal(data, &report); err != nil {
		return BrowserAgentBusinessAcceptanceReport{}, fmt.Errorf("decode browser-agent business acceptance report: %w", err)
	}
	if report.SchemaVersion != "cascade.browser_agent_business_acceptance.v1" || report.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 || report.StrictGate != "passed" || len(report.Stages) != 5 {
		return BrowserAgentBusinessAcceptanceReport{}, errors.New("browser-agent business acceptance report is incomplete")
	}
	for _, stage := range report.Stages {
		if stage.Verdict != "passed" || !stage.ActionExecuted {
			return BrowserAgentBusinessAcceptanceReport{}, errors.New("browser-agent business acceptance report contains a failed stage")
		}
	}
	if !report.EditorMaterialization.Ready || report.EditorMaterialization.SessionID == "" {
		return BrowserAgentBusinessAcceptanceReport{}, errors.New("browser-agent business acceptance report is missing editor materialization")
	}
	return report, nil
}

func (s *Service) runBrowserAgentBusinessAcceptance(ctx context.Context, fixturePath string) (BrowserAgentBusinessAcceptanceReport, error) {
	server := httptest.NewServer(http.HandlerFunc(controlledBusinessFixtureHandler))
	defer server.Close()
	pkg, err := controlledBusinessAcceptancePackage(fixturePath, server.URL)
	if err != nil {
		return BrowserAgentBusinessAcceptanceReport{}, err
	}
	run, err := s.runProtocolAcceptanceScenario(ctx, pkg)
	if err != nil {
		return BrowserAgentBusinessAcceptanceReport{}, err
	}
	materialization, err := s.GetEditorSessionMaterialization(ctx, run.Result)
	if err != nil {
		return BrowserAgentBusinessAcceptanceReport{}, err
	}
	report := BrowserAgentBusinessAcceptanceReport{
		SchemaVersion: "cascade.browser_agent_business_acceptance.v1",
		GeneratedAt:   timeNowUTC(), Runtime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1,
		StrictGate: "passed", PackageID: pkg.PackageID,
		BusinessFlow: "进入工作台 → 输入项目名 → 选择构建模式 → 提交构建 → 验证构建结果",
		Stages:       controlledBusinessAcceptanceStages(pkg, run), EditorMaterialization: materialization,
	}
	if run.Status.Status != model.ExchangePackageStatusCompleted || run.Result.Status != model.RecordingResultStatusGenerated || !hasStrictBrowserAgentValidationReports(run.Result, len(report.Stages)) || !protocolAcceptanceHasArtifact(run.Result, "raw_recording") || !protocolAcceptanceHasArtifact(run.Result, "browser_trace") || !protocolAcceptanceHasArtifact(run.Result, "demo_video") || !materialization.Ready {
		report.StrictGate = "failed"
	}
	for _, stage := range report.Stages {
		if stage.Verdict != "passed" {
			report.StrictGate = "failed"
		}
	}
	if report.StrictGate != "passed" {
		return report, nil
	}
	return report, nil
}

func controlledBusinessFixtureHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Controlled Builder</title><style>body{font-family:Arial,sans-serif;margin:48px;background:#101827;color:#edf2ff}main{max-width:760px;padding:32px;background:#18243a;border-radius:18px}label,input,button{font-size:18px}input{display:block;width:420px;margin:10px 0 22px;padding:10px}button{padding:10px 16px;margin-right:12px}small{color:#adc2e8}</style></head><body><main aria-label="Builder workspace"><p>Controlled business fixture</p><h1>Create project</h1><label>Project name<input data-testid="project-name-input" aria-label="Project name" value=""></label><button data-testid="build-mode">Build mode</button><span data-testid="mode-status">Mode not selected</span><button data-testid="start-build">Start build</button></main><script>const mode=document.querySelector('[data-testid=build-mode]');mode.onclick=()=>document.querySelector('[data-testid=mode-status]').textContent='Build mode selected';document.querySelector('[data-testid=start-build]').onclick=()=>{const name=document.querySelector('[data-testid=project-name-input]').value||'Untitled';history.pushState({},'', '/project/demo-tetris');document.body.innerHTML='<main aria-label="Build result"><p>Project detail</p><h1>Build in progress</h1><p data-testid="build-summary">Build started for '+name+'</p><ol data-testid="agent-build-log"><li>Plan accepted</li><li>Agent is preparing the build</li></ol></main>'};</script></body></html>`))
}

type controlledBusinessStageSpec struct {
	NodeID, StageID, Title, Objective, Intent, Route, Success string
	Kind                                                      model.BusinessStageKind
	RouteState                                                model.BusinessRouteState
	Action                                                    model.BrowserAgentInteraction
	Target                                                    model.BrowserAgentTargetContract
	Component                                                 model.BrowserAgentComponentTarget
	Validation                                                model.ValidationSpec
}

func controlledBusinessAcceptancePackage(fixturePath, baseURL string) (model.ClientExecutionPackage, error) {
	base, err := protocolAcceptancePackage(fixturePath, baseURL)
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	domain := parsed.Hostname()
	if domain == "" {
		return model.ClientExecutionPackage{}, errors.New("controlled business fixture host is missing")
	}
	const projectName = "Tetris Launch"
	base.PackageID, base.ProjectID = "pkg_controlled_business_outline", "project_controlled_business"
	base.ProjectContextSummary.ContextID = "ctx_controlled_business"
	base.ProjectContextSummary.Name = "Controlled project-builder business flow"
	base.ProjectContextSummary.ProductURL = baseURL
	base.RecordingRunSpec.RunID, base.RecordingRunSpec.BaseURL, base.RecordingRunSpec.AllowedDomains = "run_controlled_business", baseURL, []string{domain}
	base.RecordingRunSpec.Timeline.TargetDurationSec = 8
	// This fixture exercises the production delivery contract: an MP4 final
	// output that can later become a normalized source-reference for Doubao.
	base.RecordingRunSpec.Outputs.OutputFormats = []string{"mp4"}
	base.CredentialGrants = nil
	base.Metadata = map[string]any{"dev_plaintext_upload_mode": true, "producer": "server_controlled_business_acceptance", "runtime": model.ExecutableScriptRuntimeBrowserAgentOutlineV1}

	evidence := model.EvidenceRef{ID: "ev_controlled_builder_fixture", Kind: "fixture_source", Summary: "Server-owned controlled project-builder page", Confidence: 1}
	specs := []controlledBusinessStageSpec{
		{NodeID: "node_open_workspace", StageID: "stage_open_workspace", Title: "Open project workspace", Objective: "Project workspace is visible", Intent: "Enter the approved project creation workspace.", Route: "/app", Success: "Create project page is visible", Kind: model.BusinessStageKindSessionSetup, RouteState: model.BusinessRouteStateWorkspace, Action: model.BrowserAgentInteraction{Kind: model.GraphActionNavigate, Target: model.ActionTarget{URL: baseURL + "/app"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "approved_route_only"}, Target: model.BrowserAgentTargetContract{SemanticID: "target_workspace", Purpose: "Project workspace", AllowedRoles: []string{"main"}, AllowedNames: []string{"Builder workspace"}, ComponentRef: "component:workspace", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Component: model.BrowserAgentComponentTarget{ComponentRef: "component:workspace", Role: "main", Name: "Builder workspace", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Validation: model.ValidationSpec{ID: "validation_workspace_route", Kind: "url_matches", Target: model.ActionTarget{URL: baseURL + "/app"}, Required: true, EvidenceRefs: []model.EvidenceRef{evidence}}},
		{NodeID: "node_fill_project_name", StageID: "stage_fill_project_name", Title: "Enter project name", Objective: "Project name is filled", Intent: "Provide the App-approved project name without changing its value.", Route: "/app", Success: "Project name equals Tetris Launch", Kind: model.BusinessStageKindBusinessInput, RouteState: model.BusinessRouteStateCreationFlow, Action: model.BrowserAgentInteraction{Kind: model.GraphActionFill, Target: model.ActionTarget{TestID: "project-name-input", Label: "Project name"}, Value: projectName, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "prefer_testid_then_label"}, Target: model.BrowserAgentTargetContract{SemanticID: "target_project_name", Purpose: "Project name input", AllowedRoles: []string{"textbox"}, AllowedNames: []string{"Project name"}, ComponentRef: "component:project-name", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Component: model.BrowserAgentComponentTarget{ComponentRef: "component:project-name", Role: "textbox", Name: "Project name", Label: "Project name", TestID: "project-name-input", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Validation: model.ValidationSpec{ID: "validation_project_name", Kind: "value_equals", Target: model.ActionTarget{TestID: "project-name-input"}, Expected: projectName, Required: true, EvidenceRefs: []model.EvidenceRef{evidence}}},
		{NodeID: "node_select_build_mode", StageID: "stage_select_build_mode", Title: "Select build mode", Objective: "Build mode is selected", Intent: "Select the approved non-destructive build mode.", Route: "/app", Success: "Build mode selected", Kind: model.BusinessStageKindModeSelection, RouteState: model.BusinessRouteStateCreationFlow, Action: model.BrowserAgentInteraction{Kind: model.GraphActionClick, Target: model.ActionTarget{TestID: "build-mode"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "prefer_testid_then_role_name"}, Target: model.BrowserAgentTargetContract{SemanticID: "target_build_mode", Purpose: "Build mode selector", AllowedRoles: []string{"button"}, AllowedNames: []string{"Build mode"}, ComponentRef: "component:build-mode", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Component: model.BrowserAgentComponentTarget{ComponentRef: "component:build-mode", Role: "button", Name: "Build mode", TestID: "build-mode", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Validation: model.ValidationSpec{ID: "validation_build_mode", Kind: "text_contains", Assertion: "Build mode selected", Required: true, EvidenceRefs: []model.EvidenceRef{evidence}}},
		{NodeID: "node_submit_build", StageID: "stage_submit_build", Title: "Start build", Objective: "Build request enters project detail", Intent: "Submit the approved project-build action in the controlled fixture.", Route: "/app", Success: "Build result route is visible", Kind: model.BusinessStageKindBusinessSubmit, RouteState: model.BusinessRouteStateCreationFlow, Action: model.BrowserAgentInteraction{Kind: model.GraphActionClick, Target: model.ActionTarget{TestID: "start-build"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "prefer_testid_then_role_name"}, Target: model.BrowserAgentTargetContract{SemanticID: "target_start_build", Purpose: "Start controlled build", AllowedRoles: []string{"button"}, AllowedNames: []string{"Start build"}, ComponentRef: "component:start-build", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Component: model.BrowserAgentComponentTarget{ComponentRef: "component:start-build", Role: "button", Name: "Start build", TestID: "start-build", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Validation: model.ValidationSpec{ID: "validation_build_route", Kind: "url_matches", Target: model.ActionTarget{URL: baseURL + "/project/demo-tetris"}, Required: true, EvidenceRefs: []model.EvidenceRef{evidence}}},
		{NodeID: "node_verify_build_result", StageID: "stage_verify_build_result", Title: "Verify build result", Objective: "Build result is observable", Intent: "Verify the final business outcome from live browser evidence.", Route: "/project/demo-tetris", Success: "Build in progress is visible", Kind: model.BusinessStageKindFinalObserve, RouteState: model.BusinessRouteStateBuildRunning, Action: model.BrowserAgentInteraction{Kind: model.GraphActionInspect, Target: model.ActionTarget{Role: "heading", Text: "Build in progress"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "prefer_role_name"}, Target: model.BrowserAgentTargetContract{SemanticID: "target_build_result", Purpose: "Build result heading", AllowedRoles: []string{"heading"}, AllowedNames: []string{"Build in progress"}, ComponentRef: "component:build-result", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Component: model.BrowserAgentComponentTarget{ComponentRef: "component:build-result", Role: "heading", Name: "Build in progress", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}, Validation: model.ValidationSpec{ID: "validation_build_summary", Kind: "text_contains", Assertion: "Build started for " + projectName, Required: true, EvidenceRefs: []model.EvidenceRef{evidence}}},
	}
	applyControlledBusinessStages(&base, specs, baseURL, domain, evidence)
	if err := normalizeClientExecutionPackageForUpload(&base); err != nil {
		return model.ClientExecutionPackage{}, err
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(&base); err != nil {
		return model.ClientExecutionPackage{}, fmt.Errorf("controlled business outline package invalid: %w", err)
	}
	return base, nil
}

func applyControlledBusinessStages(pkg *model.ClientExecutionPackage, specs []controlledBusinessStageSpec, baseURL, domain string, evidence model.EvidenceRef) {
	if pkg == nil || pkg.ExecutableScriptBundle == nil {
		return
	}
	bundle := pkg.ExecutableScriptBundle
	bundle.ID, bundle.ProjectID, bundle.WorkflowGraphID = "bundle_controlled_business_outline", pkg.ProjectID, "graph_controlled_business"
	bundle.ScriptManifest.ScriptID = "script_controlled_business_outline"
	bundle.ScriptManifest.StepNodeIDs = nil
	bundle.ApprovalMarkdown.InlineMarkdown = "# Controlled business outline approval\n\nServer-owned fixture: project input, mode selection, build submission and result verification."
	bundle.SecurityPolicy.AllowedDomains = []string{domain}
	bundle.SecurityPolicy.ForbiddenPages = []string{"/billing", "/admin"}
	pkg.WorkflowGraph = &model.DemoWorkflowGraph{ID: bundle.WorkflowGraphID, ProjectID: pkg.ProjectID, SchemaVersion: model.DemoWorkflowGraphSchemaVersion, Version: 1, Status: model.GraphStatusApproved, Name: "Controlled project build", EntryPoint: specs[0].NodeID, Assets: &model.AssetManifest{DemoVideo60s: true, ScreenshotPack: true, StepByStepDocs: true, TargetDurationSec: 8, RequestedAssets: []model.AssetRequest{{ID: "final_demo_mp4", Kind: "demo_video", Format: "mp4", DurationSec: 8, Required: true}}}, EvidenceRefs: []model.EvidenceRef{evidence}}
	bundle.PlanJSON.ID, bundle.PlanJSON.ProjectID, bundle.PlanJSON.WorkflowGraphID, bundle.PlanJSON.GraphVersion = "script_doc_controlled_business", pkg.ProjectID, bundle.WorkflowGraphID, 1
	bundle.PlanJSON.Title, bundle.PlanJSON.Summary = "Controlled project build", "Formal browser-agent outline for a Server-owned business fixture."
	bundle.PlanJSON.RecordingRunSpec = pkg.RecordingRunSpec
	bundle.PlanJSON.SafetyPolicy.AllowedDomains = []string{domain}
	bundle.PlanJSON.SafetyPolicy.ForbiddenPages = []string{"/billing", "/admin"}
	bundle.StageApprovalPlan.ID, bundle.StageApprovalPlan.ProjectID, bundle.StageApprovalPlan.WorkflowGraphID = "stage_plan_controlled_business", pkg.ProjectID, bundle.WorkflowGraphID
	bundle.StageApprovalPlan.Title, bundle.StageApprovalPlan.Summary = "Controlled project build", "App-style business facts used only by Server acceptance."
	bundle.StageApprovalPlan.SafetyPolicy.AllowedDomains = []string{domain}
	bundle.StageApprovalPlan.SafetyPolicy.ForbiddenPages = []string{"/billing", "/admin"}
	bundle.ScriptOutline.ID, bundle.ScriptOutline.ProjectID, bundle.ScriptOutline.WorkflowGraphID = "outline_controlled_business", pkg.ProjectID, bundle.WorkflowGraphID
	bundle.ScriptOutline.BaseURL, bundle.ScriptOutline.ProductOrigin = baseURL, baseURL
	bundle.ScriptOutline.Summary = "Server may only adapt selector and wait details inside this controlled project-builder flow."
	bundle.ScriptOutline.AllowedExplorationScope.AllowedOrigins = []string{baseURL}
	// "/" is the worker's session entry: the worker opens the package base URL
	// before the first stage executes, so the root route belongs to the
	// approved navigation scope exactly like production packages do
	// (script_packager includes RecordingRunSpec.BaseURL in allowed routes).
	bundle.ScriptOutline.AllowedExplorationScope.AllowedRoutes = []string{"/", "/app", "/project/demo-tetris"}
	bundle.ScriptOutline.AllowedExplorationScope.ForbiddenPathPrefixes = []string{"/billing", "/admin", "/v1"}
	bundle.ScriptOutline.AllowedExplorationScope.ForbiddenKeywords = []string{"delete", "payment", "api key"}
	bundle.ScriptOutline.AllowedExplorationScope.AllowNonDestructive = true
	bundle.AgentPromptPolicy.ProjectID, bundle.AgentPromptPolicy.WorkflowGraphID = pkg.ProjectID, bundle.WorkflowGraphID
	bundle.AgentPromptPolicy.SafetyBoundaries = []string{"allowed_domains=" + domain, "forbidden_pages=/billing,/admin"}
	bundle.BrowserAgentContract.ProjectID, bundle.BrowserAgentContract.WorkflowGraphID = pkg.ProjectID, bundle.WorkflowGraphID
	bundle.UnderstandingDossier = nil
	bundle.UnderstandingDossierRef = nil
	pkg.ProductMapSummary = model.ProductMapSummary{}
	pkg.EvidenceBundle.EvidenceRefs = []model.EvidenceRef{evidence}
	pkg.EvidenceBundle.SourceTrees = nil
	pkg.SafetyReport.HumanApproval.ReviewedNodeIDs = nil
	pkg.SafetyReport.HumanApproval.Notes = []string{"Server-owned controlled business fixture; no App or customer facts are used."}

	bundle.PlanJSON.Steps = nil
	bundle.StageApprovalPlan.Stages = nil
	bundle.ScriptOutline.Stages = nil
	pkg.WorkflowGraph.Nodes = nil
	pkg.WorkflowGraph.Edges = nil
	for index, spec := range specs {
		order := index + 1
		targetContract := spec.Target
		urlValue := ""
		if spec.Action.Kind == model.GraphActionNavigate {
			urlValue = spec.Action.Target.URL
		}
		pageTarget := model.ScriptPageTarget{URL: urlValue, Selector: spec.Action.Target.Selector}
		if spec.Action.Target.TestID != "" {
			pageTarget.Selector = "[data-testid='" + spec.Action.Target.TestID + "']"
		}
		pkg.WorkflowGraph.Nodes = append(pkg.WorkflowGraph.Nodes, &model.GraphNode{ID: spec.NodeID, Type: model.GraphNodeTypeAction, Title: spec.Title, ExpectedOutcome: spec.Success, ActionSpec: &model.GraphAction{Type: spec.Action.Kind, Target: spec.Action.Target, Value: spec.Action.Value}, Validations: []model.ValidationSpec{spec.Validation}, Capture: &model.CaptureSpec{Screenshot: true, Video: true}, EvidenceRefs: []model.EvidenceRef{evidence}, DurationHintMS: 250})
		bundle.ScriptManifest.StepNodeIDs = append(bundle.ScriptManifest.StepNodeIDs, spec.NodeID)
		bundle.PlanJSON.Steps = append(bundle.PlanJSON.Steps, model.ScriptStep{ID: "step_" + strings.TrimPrefix(spec.NodeID, "node_"), Order: order, NodeID: spec.NodeID, Title: spec.Title, PageTarget: pageTarget, Action: model.ScriptActionInstruction{Type: spec.Action.Kind, Target: spec.Action.Target, Value: spec.Action.Value}, TargetContract: &targetContract, ExpectedOutcome: spec.Success, Validations: []model.ValidationSpec{spec.Validation}, Capture: model.CaptureSpec{Screenshot: true, Video: true}, Timing: model.NodeTimingHint{NodeID: spec.NodeID, DurationMS: 250}, Narrative: model.NarrativeCue{Title: spec.Title}, EvidenceRefs: []model.EvidenceRef{evidence}, Blocking: true})
		entryRoute := spec.Route
		stage := model.StageApprovalStage{ID: spec.StageID, Order: order, NodeID: spec.NodeID, StageKind: spec.Kind, RouteState: spec.RouteState, Title: spec.Title, Objective: spec.Objective, BusinessIntent: spec.Intent, DurationMS: 250, EntryRoute: entryRoute, TargetRoute: spec.Route, TargetURL: urlValue, ComponentRefs: []string{targetContract.ComponentRef}, Interaction: spec.Action, TargetContract: &targetContract, SuccessState: spec.Success, WaitConditions: spec.Action.WaitConditions, CapturePoints: []string{"after_verified_action"}, CapturePlan: &model.BrowserAgentCapturePlan{Intent: "record_verified_business_state", PrimaryArtifact: "screenshot", MinDurationMS: 250}, EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}
		bundle.StageApprovalPlan.Stages = append(bundle.StageApprovalPlan.Stages, stage)
		outline := model.BrowserAgentOutlineStage{ID: "outline_" + strings.TrimPrefix(spec.NodeID, "node_"), StageID: spec.StageID, Order: order, NodeID: spec.NodeID, StageKind: spec.Kind, RouteState: spec.RouteState, Objective: spec.Objective, EntryRoute: entryRoute, Route: spec.Route, URL: urlValue, Components: []model.BrowserAgentComponentTarget{spec.Component}, Interactions: []model.BrowserAgentInteraction{spec.Action}, TargetContract: &targetContract, WaitConditions: spec.Action.WaitConditions, CapturePoints: []string{"after_verified_action"}, CapturePlan: &model.BrowserAgentCapturePlan{Intent: "record_verified_business_state", PrimaryArtifact: "screenshot", MinDurationMS: 250}, SuccessState: spec.Success, DurationMS: 250, CanModify: []string{"selector", "wait_conditions", "capture_plan"}, MustPreserve: []string{"objective", "stage order", "input semantics", "allowed domains"}, EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1}
		bundle.ScriptOutline.Stages = append(bundle.ScriptOutline.Stages, outline)
		if index > 0 {
			pkg.WorkflowGraph.Edges = append(pkg.WorkflowGraph.Edges, &model.GraphEdge{ID: fmt.Sprintf("edge_%02d", order-1), FromNode: specs[index-1].NodeID, ToNode: spec.NodeID, EvidenceRefs: []model.EvidenceRef{evidence}})
		}
	}
}

func controlledBusinessAcceptanceStages(pkg model.ClientExecutionPackage, run protocolAcceptanceRun) []BrowserAgentAcceptanceScenario {
	stages := make([]BrowserAgentAcceptanceScenario, 0, len(pkg.ExecutableScriptBundle.PlanJSON.Steps))
	for _, step := range pkg.ExecutableScriptBundle.PlanJSON.Steps {
		reportFound := false
		for _, report := range run.Result.ValidationReports {
			if report.NodeID == step.NodeID && report.Decision == model.ValidationDecisionContinue {
				reportFound = true
				break
			}
		}
		passed := run.Status.Status == model.ExchangePackageStatusCompleted && reportFound
		stages = append(stages, BrowserAgentAcceptanceScenario{ID: step.NodeID, Description: step.Title + "：" + step.ExpectedOutcome, Expected: step.ExpectedOutcome, Actual: acceptanceActual(passed), Verdict: acceptanceVerdict(passed), ActionExecuted: true, Evidence: protocolAcceptanceArtifacts(run.Result), Assertions: []BrowserAgentAcceptanceCheck{{Kind: "required_outcome_validation", Passed: reportFound, Actual: acceptanceActual(reportFound)}}})
	}
	return stages
}
