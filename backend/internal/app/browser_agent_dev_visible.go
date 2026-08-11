package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

// DevVisibleBrowserAgentPrepareRequest is a local test-only handoff. It opens
// a brand-new visible browser profile; it deliberately cannot accept cookies,
// credentials, storage snapshots, or a production execution package.
type DevVisibleBrowserAgentPrepareRequest struct {
	TargetURL     string   `json:"target_url"`
	MaskSelectors []string `json:"mask_selectors,omitempty"`
	DevTestAck    bool     `json:"dev_test_ack"`
}

type DevVisibleBrowserAgentView struct {
	DevTestOnly  bool                                  `json:"dev_test_only"`
	SessionID    string                                `json:"session_id,omitempty"`
	Status       string                                `json:"status"`
	TargetOrigin string                                `json:"target_origin,omitempty"`
	ActualURL    string                                `json:"actual_url,omitempty"`
	PageTitle    string                                `json:"page_title,omitempty"`
	ExpiresAt    time.Time                             `json:"expires_at,omitempty"`
	ManualAction string                                `json:"manual_action,omitempty"`
	Message      string                                `json:"message"`
	Package      *DevVisibleBrowserAgentPackageBinding `json:"package,omitempty"`
	Result       *DevVisibleBrowserAgentResult         `json:"result,omitempty"`
}

type DevVisibleBrowserAgentExecuteRequest struct {
	Package    model.ClientExecutionPackage `json:"package"`
	DevTestAck bool                         `json:"dev_test_ack"`
}

// DevVisibleBrowserAgentFixedExecuteRequest cannot carry a package or action.
// It exists only to execute the fixed dev/test acceptance package in-process,
// avoiding client-side JSON reserialization of protocol hash fields.
type DevVisibleBrowserAgentFixedExecuteRequest struct {
	DevTestAck bool `json:"dev_test_ack"`
}

// DevVisibleBrowserAgentResult contains only post-login local test artifacts.
// The test run is never uploaded to Exchange or reported as production work.
type DevVisibleBrowserAgentResult struct {
	Status                 string              `json:"status"`
	ResultID               string              `json:"result_id,omitempty"`
	VideoPath              string              `json:"video_path,omitempty"`
	VideoURL               string              `json:"video_url,omitempty"`
	StageEventPath         string              `json:"stage_event_path,omitempty"`
	ResultPackagePath      string              `json:"result_package_path,omitempty"`
	ValidationCount        int                 `json:"validation_count,omitempty"`
	Artifacts              []model.ArtifactRef `json:"artifacts,omitempty"`
	DevTestOnly            bool                `json:"dev_test_only,omitempty"`
	NotForExchangeUpload   bool                `json:"not_for_exchange_upload,omitempty"`
	TestOnlyWaiver         bool                `json:"test_only_waiver,omitempty"`
	FormalExchange         bool                `json:"formal_exchange"`
	AppGenerated           bool                `json:"app_generated,omitempty"`
	TransportAuthenticated bool                `json:"transport_authenticated"`
	WaiverID               string              `json:"waiver_id,omitempty"`
}

// DevVisibleBrowserAgentPackageBinding proves only that an already-approved
// App package is compatible with the current real-page session. It does not
// retain the package and cannot execute an action.
type DevVisibleBrowserAgentPackageBinding struct {
	PackageID  string `json:"package_id"`
	Runtime    string `json:"runtime"`
	StageCount int    `json:"stage_count"`
}

type DevVisibleBrowserAgentBindPackageRequest struct {
	Package    model.ClientExecutionPackage `json:"package"`
	DevTestAck bool                         `json:"dev_test_ack"`
}

// DevVisibleBrowserAgentApprovedPackageRequest builds one fixed, local-test
// package for the explicitly approved real-product acceptance flow. It is not
// an App upload endpoint and cannot be used to supply arbitrary actions.
type DevVisibleBrowserAgentApprovedPackageRequest struct {
	TargetURL  string `json:"target_url"`
	DevTestAck bool   `json:"dev_test_ack"`
}

// DevVisibleBrowserAgentApprovedPackageView labels the package as local-test
// only so it cannot be confused with an App-generated package.
type DevVisibleBrowserAgentApprovedPackageView struct {
	DevTestOnly bool                         `json:"dev_test_only"`
	Message     string                       `json:"message"`
	Package     model.ClientExecutionPackage `json:"package"`
}

type devVisibleBrowserAgentSession struct {
	session      *driver.BrowserAgentWorkerSession
	targetOrigin string
	expiresAt    time.Time
}

// devVisibleBrowserAgentManager is intentionally not reused by the normal
// Outline runner. Holding this session is what lets a human log in manually
// without Server ever observing or importing authentication state.
type devVisibleBrowserAgentManager struct {
	service  *Service
	mu       sync.Mutex
	sessions map[string]devVisibleBrowserAgentSession
}

func newDevVisibleBrowserAgentManager(service *Service) *devVisibleBrowserAgentManager {
	return &devVisibleBrowserAgentManager{service: service, sessions: map[string]devVisibleBrowserAgentSession{}}
}

func (m *devVisibleBrowserAgentManager) Prepare(ctx context.Context, request DevVisibleBrowserAgentPrepareRequest) (DevVisibleBrowserAgentView, error) {
	if m == nil || m.service == nil {
		return DevVisibleBrowserAgentView{}, errors.New("dev visible browser-agent manager is not configured")
	}
	if err := m.localOnlyGuard(request.DevTestAck); err != nil {
		return DevVisibleBrowserAgentView{}, err
	}
	target, err := devVisibleTargetURL(request.TargetURL)
	if err != nil {
		return DevVisibleBrowserAgentView{}, err
	}
	if !fileExists(m.service.localVideoWorkerPath()) {
		return DevVisibleBrowserAgentView{}, errors.New("video-worker build artifact is not available")
	}
	if err := checkCommandReady(m.service.nodeBinaryForExecution()); err != nil {
		return DevVisibleBrowserAgentView{}, err
	}

	identifier := fmt.Sprintf("dev_visible_%d", timeNowUTC().UnixNano())
	outputDir := filepath.Join(m.service.runtime.ArtifactRoot, "dev-test-only", "visible-browser-agent", identifier)
	recordingSensitive := false
	recordTrace := false
	worker := driver.NewBrowserAgentWorker(m.service.nodeBinaryForExecution(), m.service.localVideoWorkerPath(), m.service.videoWorkerEnvironment())
	// Browser startup on a developer workstation may consume most of the
	// request budget. Keep it separate from the real-page navigation budget.
	openCtx, cancelOpen := context.WithTimeout(ctx, 60*time.Second)
	defer cancelOpen()
	session, opened, err := worker.Open(openCtx, driver.BrowserAgentWorkerOpenRequest{
		SessionID: identifier, OutputDir: outputDir,
		Browser: driver.BrowserAgentWorkerBrowser{
			Engine: "chromium", Headless: false,
			Viewport: driver.BrowserAgentWorkerViewport{Width: 1440, Height: 900},
			// No video is recorded during manual login.
			RecordVideo: false,
		},
		AllowedDomains: []string{"127.0.0.1"}, AllowedOrigins: []string{devVisibleOrigin(target)}, AllowedRoutes: []string{"/"},
		MaskSelectors:      uniqueStrings(append(defaultDevVisibleMaskSelectors(), request.MaskSelectors...)),
		RecordingSensitive: &recordingSensitive, RecordTrace: &recordTrace,
	})
	if err != nil {
		return DevVisibleBrowserAgentView{}, fmt.Errorf("start isolated visible browser: %w", err)
	}
	navigateCtx, cancelNavigate := context.WithTimeout(ctx, 45*time.Second)
	defer cancelNavigate()
	if _, err := session.NavigateDevVisible(navigateCtx, target.String()); err != nil {
		_ = session.Abort()
		return DevVisibleBrowserAgentView{}, fmt.Errorf("navigate isolated visible browser: %w", err)
	}
	expiresAt := timeNowUTC().Add(20 * time.Minute)
	m.mu.Lock()
	m.sessions[opened.SessionID] = devVisibleBrowserAgentSession{session: session, targetOrigin: devVisibleOrigin(target), expiresAt: expiresAt}
	m.mu.Unlock()
	return m.view(ctx, opened.SessionID)
}

func (m *devVisibleBrowserAgentManager) Continue(ctx context.Context, sessionID string) (DevVisibleBrowserAgentView, error) {
	if err := m.localOnlyGuard(true); err != nil {
		return DevVisibleBrowserAgentView{}, err
	}
	return m.view(ctx, sessionID)
}

func (m *devVisibleBrowserAgentManager) Abort(ctx context.Context, sessionID string) (DevVisibleBrowserAgentView, error) {
	session, ok := m.take(sessionID)
	if !ok {
		return DevVisibleBrowserAgentView{}, errors.New("dev visible browser-agent session was not found")
	}
	_ = session.session.Abort()
	return DevVisibleBrowserAgentView{DevTestOnly: true, SessionID: sessionID, Status: "aborted", TargetOrigin: session.targetOrigin, Message: "本地测试会话已关闭；未保存 Cookie、凭据、Trace 或录屏。"}, nil
}

// BindApprovedPackage is a test-only preflight. The real package remains in
// the caller; no payload is persisted in the visible-session manager.
func (m *devVisibleBrowserAgentManager) BindApprovedPackage(ctx context.Context, sessionID string, request DevVisibleBrowserAgentBindPackageRequest) (DevVisibleBrowserAgentView, error) {
	if err := m.localOnlyGuard(request.DevTestAck); err != nil {
		return DevVisibleBrowserAgentView{}, err
	}
	view, session, err := m.readySessionView(ctx, sessionID)
	if err != nil {
		return view, err
	}
	if view.Status != "ready_for_approved_package" {
		return view, errors.New("visible browser is not ready on the real product page")
	}
	binding, err := validateDevVisiblePackageBinding(request.Package, session.targetOrigin)
	if err != nil {
		return view, err
	}
	view.Package = &binding
	view.Message = "真实来源与已审批 browser-agent-outline-v1 包已完成本地兼容性预检；未保存执行包、未执行步骤、未录屏或渲染。后续执行必须走正式 Intake、审批与 Runtime 路径。"
	return view, nil
}

// BuildApprovedLocalTestPackage constructs only the three actions explicitly
// approved for the local real-product acceptance. It is never uploaded to
// Exchange and cannot accept arbitrary business actions from a caller.
func (m *devVisibleBrowserAgentManager) BuildApprovedLocalTestPackage(ctx context.Context, request DevVisibleBrowserAgentApprovedPackageRequest) (DevVisibleBrowserAgentApprovedPackageView, error) {
	_ = ctx
	if err := m.localOnlyGuard(request.DevTestAck); err != nil {
		return DevVisibleBrowserAgentApprovedPackageView{}, err
	}
	target, err := devVisibleTargetURL(request.TargetURL)
	if err != nil {
		return DevVisibleBrowserAgentApprovedPackageView{}, err
	}
	fixturePath := m.service.browserAgentAcceptanceFixturePath()
	if fixturePath == "" || !fileExists(fixturePath) {
		return DevVisibleBrowserAgentApprovedPackageView{}, errors.New("dev visible browser-agent package fixture is not available")
	}
	pkg, err := devVisibleRealProductTestPackage(fixturePath, target.String())
	if err != nil {
		return DevVisibleBrowserAgentApprovedPackageView{}, err
	}
	return DevVisibleBrowserAgentApprovedPackageView{
		DevTestOnly: true,
		Message:     "已生成仅用于本地真实页面验收的固定三步测试包；它不会上传 Exchange，不能替代 App 正式执行包。",
		Package:     pkg,
	}, nil
}

// ExecuteFixedApprovedLocalTestPackage is intentionally narrower than the
// regular execute endpoint: its three actions are compiled in Server and never
// supplied by a client. It remains local dev/test-only and cannot upload to
// Exchange.
func (m *devVisibleBrowserAgentManager) ExecuteFixedApprovedLocalTestPackage(ctx context.Context, sessionID string, request DevVisibleBrowserAgentFixedExecuteRequest) (DevVisibleBrowserAgentView, error) {
	if err := m.localOnlyGuard(request.DevTestAck); err != nil {
		return DevVisibleBrowserAgentView{}, err
	}
	fixturePath := m.service.browserAgentAcceptanceFixturePath()
	if fixturePath == "" || !fileExists(fixturePath) {
		return DevVisibleBrowserAgentView{}, errors.New("dev visible browser-agent package fixture is not available")
	}
	m.mu.Lock()
	session, ok := m.sessions[strings.TrimSpace(sessionID)]
	m.mu.Unlock()
	if !ok {
		return DevVisibleBrowserAgentView{}, errors.New("dev visible browser-agent session was not found")
	}
	pkg, err := devVisibleRealProductTestPackage(fixturePath, session.targetOrigin+"/app")
	if err != nil {
		return DevVisibleBrowserAgentView{}, err
	}
	return m.ExecuteApprovedPackage(ctx, sessionID, DevVisibleBrowserAgentExecuteRequest{Package: pkg, DevTestAck: true})
}

func devVisibleRealProductTestPackage(fixturePath string, targetURL string) (model.ClientExecutionPackage, error) {
	target, err := devVisibleTargetURL(targetURL)
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	origin := devVisibleOrigin(target)
	pkg, err := controlledBusinessAcceptancePackage(fixturePath, origin)
	if err != nil {
		return model.ClientExecutionPackage{}, err
	}
	now := timeNowUTC()
	const idea = "贪吃蛇游戏"
	evidence := model.EvidenceRef{ID: "ev_dev_visible_real_product_actions", Kind: model.EvidenceKindUserInput, Summary: "用户明确批准的本地验收动作和已核验 data-testid", Confidence: 1}
	specs := []controlledBusinessStageSpec{
		{
			NodeID: "node_open_new_project", StageID: "stage_open_new_project", Title: "打开新建项目", Objective: "新建项目对话框可见", Intent: "仅打开真实工作台中的新建项目对话框。", Route: "/app", Success: "新建项目对话框可见", Kind: model.BusinessStageKindBusinessAction, RouteState: model.BusinessRouteStateWorkspace,
			Action:     model.BrowserAgentInteraction{Kind: model.GraphActionClick, Target: model.ActionTarget{TestID: "button-new-project"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "prefer_testid"},
			Target:     model.BrowserAgentTargetContract{SemanticID: "target_new_project", Purpose: "新建项目按钮", AllowedRoles: []string{"button"}, ComponentRef: "component:new-project", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1},
			Component:  model.BrowserAgentComponentTarget{ComponentRef: "component:new-project", Role: "button", TestID: "button-new-project", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1},
			Validation: model.ValidationSpec{ID: "validation_new_project_dialog", Kind: "element_visible", Target: model.ActionTarget{TestID: "dialog-new-project"}, Required: true, EvidenceRefs: []model.EvidenceRef{evidence}},
		},
		{
			NodeID: "node_fill_project_idea", StageID: "stage_fill_project_idea", Title: "输入项目需求", Objective: "项目需求已填入批准内容", Intent: "只在新建项目对话框中填入已批准内容，不读取或修改任何敏感字段。", Route: "/app", Success: "需求内容为贪吃蛇游戏", Kind: model.BusinessStageKindBusinessInput, RouteState: model.BusinessRouteStateCreationFlow,
			Action:     model.BrowserAgentInteraction{Kind: model.GraphActionFill, Target: model.ActionTarget{TestID: "input-project-idea"}, Value: idea, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "prefer_testid"},
			Target:     model.BrowserAgentTargetContract{SemanticID: "target_project_idea", Purpose: "项目需求输入框", AllowedRoles: []string{"textbox"}, ComponentRef: "component:project-idea", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1},
			Component:  model.BrowserAgentComponentTarget{ComponentRef: "component:project-idea", Role: "textbox", TestID: "input-project-idea", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1},
			Validation: model.ValidationSpec{ID: "validation_project_idea", Kind: "value_equals", Target: model.ActionTarget{TestID: "input-project-idea"}, Expected: idea, Required: true, EvidenceRefs: []model.EvidenceRef{evidence}},
		},
		{
			NodeID: "node_submit_project", StageID: "stage_submit_project", Title: "提交构建", Objective: "已提交批准的新建项目请求", Intent: "只点击已批准的新建项目提交按钮；不执行删除、支付、权限或导出操作。", Route: "/project", Success: "项目编辑页面可见", Kind: model.BusinessStageKindBusinessSubmit, RouteState: model.BusinessRouteStateProjectDetail,
			Action:     model.BrowserAgentInteraction{Kind: model.GraphActionClick, Target: model.ActionTarget{TestID: "button-create-project"}, NonDestructive: true, WaitConditions: []string{"wait_after_entry_at_least_250ms"}, SelectorPolicy: "prefer_testid"},
			Target:     model.BrowserAgentTargetContract{SemanticID: "target_create_project", Purpose: "新建项目提交按钮", AllowedRoles: []string{"button"}, ComponentRef: "component:create-project", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1},
			Component:  model.BrowserAgentComponentTarget{ComponentRef: "component:create-project", Role: "button", TestID: "button-create-project", EvidenceRefs: []model.EvidenceRef{evidence}, Confidence: 1},
			Validation: model.ValidationSpec{ID: "validation_project_editor", Kind: "element_visible", Target: model.ActionTarget{TestID: "text-project-name"}, Required: true, EvidenceRefs: []model.EvidenceRef{evidence}},
		},
	}
	applyControlledBusinessStages(&pkg, specs, origin, target.Hostname(), evidence)
	pkg.PackageID, pkg.ProjectID = "pkg_dev_visible_real_local_product", "project_dev_visible_real_local_product"
	pkg.CreatedAt, pkg.ApprovedAt = now, now
	pkg.ProjectContextSummary.ContextID = "ctx_dev_visible_real_local_product"
	pkg.ProjectContextSummary.Name = "Dev/test-only real local product acceptance"
	pkg.ProjectContextSummary.ProductURL = target.String()
	pkg.ProjectContextSummary.TargetAudience = "local product acceptance"
	pkg.RecordingRunSpec.RunID, pkg.RecordingRunSpec.BaseURL = "run_dev_visible_real_local_product", target.String()
	pkg.RecordingRunSpec.AllowedDomains = []string{target.Hostname()}
	pkg.RecordingRunSpec.Outputs.RawRecording, pkg.RecordingRunSpec.Outputs.FinalVideo = true, true
	pkg.RecordingRunSpec.Outputs.ScreenshotPack, pkg.RecordingRunSpec.Outputs.StepByStepDocs, pkg.RecordingRunSpec.Outputs.Trace = true, true, true
	pkg.Metadata = map[string]any{"producer": "server_dev_test_real_product_acceptance", "dev_test_only": true, "not_for_exchange_upload": true, "approved_actions": []string{"button-new-project", "input-project-idea", "button-create-project"}}
	pkg.SafetyReport.HumanApproval.ApprovalID = "dev_test_user_authorization_real_local_product"
	pkg.SafetyReport.HumanApproval.ApprovedByUserID, pkg.SafetyReport.HumanApproval.ApprovedAt = "local_dev_test_user", now
	pkg.SafetyReport.HumanApproval.Notes = []string{"Local dev/test-only package built from the user's explicit three-action authorization. Not an App-generated or Exchange-uploadable package."}
	bundle := pkg.ExecutableScriptBundle
	if bundle == nil || bundle.ScriptOutline == nil || bundle.PlanJSON == nil || bundle.StageApprovalPlan == nil || bundle.AgentPromptPolicy == nil || bundle.BrowserAgentContract == nil {
		return model.ClientExecutionPackage{}, errors.New("dev visible package bundle is incomplete")
	}
	bundle.ID, bundle.ProjectID, bundle.WorkflowGraphID = "bundle_dev_visible_real_local_product", pkg.ProjectID, "graph_dev_visible_real_local_product"
	bundle.ScriptOutline.ProjectID, bundle.ScriptOutline.WorkflowGraphID = pkg.ProjectID, bundle.WorkflowGraphID
	bundle.ScriptOutline.BaseURL, bundle.ScriptOutline.ProductOrigin = target.String(), origin
	bundle.ScriptOutline.AllowedExplorationScope.AllowedOrigins = []string{origin}
	bundle.ScriptOutline.AllowedExplorationScope.AllowedRoutes = []string{"/app", "/project"}
	bundle.ScriptOutline.AllowedExplorationScope.ForbiddenPathPrefixes = []string{"/aigc", "/.well-known", "/v1", "/execution-packages", "/app-installations", "/billing", "/settings", "/admin"}
	bundle.ScriptOutline.AllowedExplorationScope.ForbiddenKeywords = []string{"delete", "payment", "export", "token", "api key"}
	bundle.SecurityPolicy.AllowedDomains = []string{target.Hostname()}
	bundle.SecurityPolicy.ForbiddenPages = append([]string{}, bundle.ScriptOutline.AllowedExplorationScope.ForbiddenPathPrefixes...)
	bundle.SecurityPolicy.ForbiddenData = []string{"password", "email", "phone", "token", "cookie", "api key"}
	bundle.PlanJSON.ProjectID, bundle.PlanJSON.WorkflowGraphID, bundle.PlanJSON.RecordingRunSpec = pkg.ProjectID, bundle.WorkflowGraphID, pkg.RecordingRunSpec
	bundle.StageApprovalPlan.ProjectID, bundle.StageApprovalPlan.WorkflowGraphID = pkg.ProjectID, bundle.WorkflowGraphID
	bundle.StageApprovalPlan.SafetyPolicy.AllowedDomains, bundle.StageApprovalPlan.SafetyPolicy.ForbiddenPages = []string{target.Hostname()}, append([]string{}, bundle.SecurityPolicy.ForbiddenPages...)
	bundle.AgentPromptPolicy.ProjectID, bundle.AgentPromptPolicy.WorkflowGraphID = pkg.ProjectID, bundle.WorkflowGraphID
	bundle.BrowserAgentContract.ProjectID, bundle.BrowserAgentContract.WorkflowGraphID = pkg.ProjectID, bundle.WorkflowGraphID
	pkg.WorkflowGraph.ID, pkg.WorkflowGraph.ProjectID = bundle.WorkflowGraphID, pkg.ProjectID
	// The package crosses HTTP before it reaches the visible-session endpoint.
	// Rehydrate it once before hashing so the local test package has the same
	// canonical JSON shape that the normal App-to-Server handoff uses.
	encoded, err := json.Marshal(pkg)
	if err != nil {
		return model.ClientExecutionPackage{}, fmt.Errorf("encode dev visible real-product package: %w", err)
	}
	if err := json.Unmarshal(encoded, &pkg); err != nil {
		return model.ClientExecutionPackage{}, fmt.Errorf("decode dev visible real-product package: %w", err)
	}
	if err := normalizeClientExecutionPackageForUpload(&pkg); err != nil {
		return model.ClientExecutionPackage{}, err
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(&pkg); err != nil {
		return model.ClientExecutionPackage{}, fmt.Errorf("dev visible real-product package protocol validation failed: %w", err)
	}
	return pkg, nil
}

// ExecuteApprovedPackage uses the existing, manually authenticated isolated
// context only in the dev/test runtime. It runs the same compiler, policy
// guard, stage orchestration, audit events, and outcome verifier as the normal
// Outline runtime, but never submits an Exchange job or captures login data.
func (m *devVisibleBrowserAgentManager) ExecuteApprovedPackage(ctx context.Context, sessionID string, request DevVisibleBrowserAgentExecuteRequest) (DevVisibleBrowserAgentView, error) {
	return m.executePackageWithGuard(ctx, sessionID, request, contractBrowserAgentPolicyGuard{}, nil)
}

// ExecuteWaivedPackage is callable only by the Server-owned waiver manager.
// No HTTP caller can provide or mutate package JSON on this path.
func (m *devVisibleBrowserAgentManager) ExecuteWaivedPackage(ctx context.Context, sessionID string, pkg model.ClientExecutionPackage, guard BrowserAgentPolicyGuard, waiver BrowserAgentTestWaiver) (DevVisibleBrowserAgentView, error) {
	if !waiver.DevTestOnly || !waiver.NotForExchangeUpload || !waiver.TestOnlyWaiver || waiver.FormalExchange {
		return DevVisibleBrowserAgentView{}, errors.New("invalid local app-package test waiver")
	}
	return m.executePackageWithGuard(ctx, sessionID, DevVisibleBrowserAgentExecuteRequest{Package: pkg, DevTestAck: true}, guard, &waiver)
}

func (m *devVisibleBrowserAgentManager) executePackageWithGuard(ctx context.Context, sessionID string, request DevVisibleBrowserAgentExecuteRequest, guard BrowserAgentPolicyGuard, waiver *BrowserAgentTestWaiver) (DevVisibleBrowserAgentView, error) {
	if err := m.localOnlyGuard(request.DevTestAck); err != nil {
		return DevVisibleBrowserAgentView{}, err
	}
	view, session, err := m.readySessionView(ctx, sessionID)
	if err != nil {
		return view, err
	}
	if view.Status != "ready_for_approved_package" {
		return view, errors.New("visible browser is not ready on the real product page")
	}
	// A local visible session contains an authenticated browser context. It is
	// single-use even when validation, execution, or rendering fails.
	defer m.closeSession(sessionID, session)
	var binding DevVisibleBrowserAgentPackageBinding
	if waiver == nil {
		binding, err = validateDevVisiblePackageBinding(request.Package, session.targetOrigin)
	} else {
		binding, err = validateDevVisibleWaivedPackageBinding(request.Package, session.targetOrigin, *waiver)
	}
	if err != nil {
		return view, err
	}
	view.Package = &binding
	if err := m.applyApprovedExecutionPolicy(ctx, session, request.Package, waiver); err != nil {
		return view, err
	}

	startedAt := timeNowUTC()
	runID := "dev_visible_run_" + safePathSegment(sessionID)
	recordingDir := filepath.Join(m.service.runtime.ArtifactRoot, "dev-test-only", "visible-browser-agent", safePathSegment(sessionID), "execution")
	runtimeMode := "dev-visible-post-login"
	if waiver != nil {
		runID = "server_local_app_package_waiver_acceptance_" + safePathSegment(waiver.WaiverID)
		recordingDir = filepath.Join(m.service.runtime.ArtifactRoot, "dev-test-only", "app-package-waiver", safePathSegment(waiver.WaiverID), "execution")
		runtimeMode = "server-local-app-package-waiver-acceptance"
	}
	renderDir := filepath.Join(recordingDir, "render")
	plan, err := newBrowserAgentStageOrchestrator(guard).Prepare(&request.Package)
	if err != nil {
		return view, err
	}
	plan = devVisiblePostLoginRuntimePlan(plan, view.ActualURL)
	if waiver != nil {
		var applied []BrowserAgentTestWaiverNode
		plan, applied, err = applyTestOnlyWaiverRuntimeClassifications(plan, *waiver)
		if err != nil {
			return view, fmt.Errorf("apply local test-waiver runtime classifications: %w", err)
		}
		if err := appendBrowserAgentTestWaiverAudit(waiver.AuditLogPath, map[string]any{
			"event": "waiver_runtime_classifications_applied", "waiver_id": waiver.WaiverID,
			"package_id": waiver.PackageID, "bundle_hash_sha256": waiver.BundleHashSHA256, "plan_hash_sha256": waiver.PlanHashSHA256,
			"classifications": applied, "before": false, "after": true, "runtime_copy_only": true,
			"original_package_unchanged": true, "formal_runtime_unchanged": true, "occurred_at": timeNowUTC(),
		}); err != nil {
			return view, fmt.Errorf("audit local test-waiver runtime classifications: %w", err)
		}
	}
	plan.RunID = runID
	eventSink, err := newStageEventAuditLog(recordingDir, runID)
	if err != nil {
		return view, err
	}
	verifier := m.service.browserAgentOutcomeVerifierSnapshot()
	if verifier == nil {
		verifier = newDeterministicBrowserAgentStageVerifier(&request.Package)
	}
	validationContext := validationContextFromPackage(&request.Package)
	validationContext.RunID = plan.RunID
	fullVerifier, _ := verifier.(OutcomeVerifier)
	preReports := []model.ValidationReport{}
	if fullVerifier != nil {
		pre, verifyErr := fullVerifier.ValidateBeforeExecution(ctx, validationContext)
		if verifyErr != nil || pre.Validate() != nil || pre.Decision != model.ValidationDecisionContinue {
			if verifyErr != nil {
				return view, fmt.Errorf("visible execution pre-verification failed: %w", verifyErr)
			}
			return view, errors.New("visible execution pre-verification stopped the approved package")
		}
		preReports = append(preReports, pre)
	}
	stageRuntime := &localBrowserAgentStageRuntime{session: session.session, stageCount: len(plan.Stages), artifacts: map[string]model.ArtifactRef{}}
	runResult, runErr := newBrowserAgentStageOrchestratorWithVerifier(guard, verifier).Run(ctx, plan, stageRuntime, stageRuntime, eventSink)
	if runResult.AuditError != nil {
		return view, fmt.Errorf("visible execution audit event failure: %w", runResult.AuditError)
	}
	completedAt := timeNowUTC()
	artifacts := mapBrowserAgentArtifacts(stageRuntime.artifacts)
	recordResult := executor.RecordResult{
		GeneratedAssets: artifacts,
		StepResults:     browserAgentStepResults(plan, runResult.Events, stageRuntime.artifacts),
		WorkerID:        "video-worker-browser-agent-dev-visible",
		RuntimeVersions: map[string]string{"runner": "playwright-browser-agent", "mode": runtimeMode},
		SandboxMetadata: browserAgentSandboxMetadata(&request.Package, map[string]string{"runner": "playwright-browser-agent", "mode": runtimeMode}),
		StartedAt:       startedAt, CompletedAt: completedAt,
	}
	if runErr != nil {
		recordResult.StepResults = browserAgentFailedStepResults(plan, runResult.Events, stageRuntime.artifacts, runErr)
		recordResult.FailureDiagnostic = browserAgentFailureDiagnostic(BrowserAgentOutlineRunRequest{Package: &request.Package, RuntimePlan: plan, CloudJobID: runID}, runResult.Events, stageRuntime.artifacts, runErr, completedAt)
	}
	var result model.RecordingResultPackage
	var packageErr error
	if waiver == nil {
		result, packageErr = executor.NewRecordingResultPackageFromRecordResult(&request.Package, recordResult, runID, completedAt)
	} else {
		result, packageErr = executor.NewLocalTestRecordingResultPackageFromRecordResult(&request.Package, recordResult, runID, completedAt, executor.LocalTestRecordingResultPackageOptions{
			WaiverID: waiver.WaiverID, SourceBundleHashSHA256: waiver.BundleHashSHA256, SourcePlanHashSHA256: waiver.PlanHashSHA256,
		})
	}
	if packageErr != nil {
		return view, fmt.Errorf("visible execution result packaging failed: %w", packageErr)
	}
	// The stage log is part of the result contract, not merely a side effect
	// left on disk. Register its immutable local artifact reference before the
	// result is exposed so a failed or successful run is fully traceable.
	if eventSink != nil && eventSink.Count() > 0 {
		stageArtifact, artifactErr := eventSink.ArtifactRef()
		if artifactErr != nil {
			return view, fmt.Errorf("visible execution stage log packaging failed: %w", artifactErr)
		}
		result.StageEventLogRef = &stageArtifact
	}
	result.ValidationReports = append(result.ValidationReports, preReports...)
	result.ValidationReports = append(result.ValidationReports, runResult.ValidationReports...)
	result.PatchLedger = append(result.PatchLedger, runResult.PatchLedger...)
	if fullVerifier != nil && result.Status != model.RecordingResultStatusFailed {
		post, verifyErr := fullVerifier.ValidatePostExecution(ctx, validationContext, result, runResult.Events)
		if verifyErr != nil || post.Validate() != nil || post.Decision != model.ValidationDecisionContinue {
			if verifyErr != nil {
				return view, fmt.Errorf("visible execution post-verification failed: %w", verifyErr)
			}
			return view, errors.New("visible execution post-verification stopped the result")
		}
		result.ValidationReports = append(result.ValidationReports, post)
	}
	if result.Status == model.RecordingResultStatusGenerated && request.Package.RecordingRunSpec.Outputs.FinalVideo {
		renderService := m.service.editorWorker
		if _, _, err := executor.RenderClientExecutionRecordingResult(ctx, renderService, &request.Package, &result, renderDir, nil); err != nil {
			return view, fmt.Errorf("visible execution video render failed: %w", err)
		}
	}
	// Build and persist the Replay Manifest so the failure scene can be
	// reconstructed from local artifacts without replaying the run.
	replayManifest, manifestErr := BuildReplayManifest(BuildReplayManifestInput{
		Result:    result,
		Events:    runResult.Events,
		Package:   request.Package,
		RunID:     runID,
		EventDir:  recordingDir,
		Waiver:    waiver,
		CreatedAt: timeNowUTC(),
	})
	if manifestErr != nil {
		return view, fmt.Errorf("visible execution replay manifest failed: %w", manifestErr)
	}
	if err := AttachReplayManifestArtifact(&result, replayManifest); err != nil {
		return view, fmt.Errorf("visible execution replay manifest artifact failed: %w", err)
	}
	// Persist the exact result package (including StepResults, ValidationReports,
	// failure diagnostics, stage-log reference and replay manifest) for local
	// audit/replay.
	resultPath, persistErr := persistLocalVisibleResultPackage(recordingDir, result)
	if persistErr != nil {
		return view, fmt.Errorf("visible execution result package persistence failed: %w", persistErr)
	}
	view.Result = visibleExecutionResult(result, recordingDir)
	view.Result.ResultPackagePath = resultPath
	if waiver != nil {
		view.Result.DevTestOnly = true
		view.Result.NotForExchangeUpload = true
		view.Result.TestOnlyWaiver = true
		view.Result.FormalExchange = false
		view.Result.AppGenerated = true
		view.Result.TransportAuthenticated = false
		view.Result.WaiverID = waiver.WaiverID
	}
	// The session held authentication state only for this local test. Close it
	// after the bounded execution so it cannot be reused by a later request.
	if runErr != nil {
		view.Result.Status = "failed"
		view.Message = "真实页面执行已停止并保留脱敏诊断；未生成可交付视频。"
		return view, nil
	}
	if view.Result.VideoPath == "" {
		view.Result.Status = "completed_without_video"
		view.Message = "真实页面步骤已完成并通过验证，但执行包未请求最终视频。"
		return view, nil
	}
	view.Result.Status = "completed"
	view.Message = "真实页面已按审批包完成步骤；MP4 仅由登录后的脱敏步骤截图合成。"
	return view, nil
}

func devVisiblePostLoginRuntimePlan(plan BrowserAgentRuntimePlan, actualURL string) BrowserAgentRuntimePlan {
	if !sameDevVisibleRoute(actualURL, "/app") {
		return plan
	}
	plan.Stages = append([]BrowserAgentRuntimeStage{}, plan.Stages...)
	for index := range plan.Stages {
		stage := &plan.Stages[index]
		if stage.Order == 1 && stage.StageKind == model.BusinessStageKindSessionSetup && sameDevVisibleRoute(stage.URL, "/app") {
			stage.ManualSessionCheckpoint = true
		}
	}
	return plan
}

func sameDevVisibleRoute(value string, expectedPath string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err == nil && parsed.Path != "" {
		return strings.EqualFold(strings.TrimRight(parsed.Path, "/"), strings.TrimRight(expectedPath, "/"))
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(expectedPath))
}

func validateDevVisibleWaivedPackageBinding(pkg model.ClientExecutionPackage, targetOrigin string, waiver BrowserAgentTestWaiver) (DevVisibleBrowserAgentPackageBinding, error) {
	if pkg.PackageID != waiver.PackageID || pkg.ProjectID != waiver.ProjectID || pkg.ExecutableScriptBundle == nil {
		return DevVisibleBrowserAgentPackageBinding{}, errors.New("test waiver package or project identity mismatch")
	}
	bundle := pkg.ExecutableScriptBundle
	if bundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 || bundle.Reproducibility.BundleHashSHA256 != waiver.BundleHashSHA256 || bundle.Reproducibility.PlanHashSHA256 != waiver.PlanHashSHA256 {
		return DevVisibleBrowserAgentPackageBinding{}, errors.New("test waiver runtime or protocol hash mismatch")
	}
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		return DevVisibleBrowserAgentPackageBinding{}, fmt.Errorf("waived App draft plan compilation failed: %w", err)
	}
	baseURL, err := url.Parse(pkg.RecordingRunSpec.BaseURL)
	if err != nil || devVisibleOrigin(baseURL) != targetOrigin || waiver.AllowedOrigin != targetOrigin {
		return DevVisibleBrowserAgentPackageBinding{}, errors.New("waived App draft does not match the visible real-page origin")
	}
	if !containsExactString(plan.ExplorationScope.AllowedOrigins, targetOrigin) {
		return DevVisibleBrowserAgentPackageBinding{}, errors.New("waived App draft allowed_origins does not include the visible real-page origin")
	}
	if !containsExactString(pkg.RecordingRunSpec.AllowedDomains, baseURL.Hostname()) || !containsExactString(bundle.SecurityPolicy.AllowedDomains, baseURL.Hostname()) {
		return DevVisibleBrowserAgentPackageBinding{}, errors.New("waived App draft allowed_domains does not include the visible real-page host")
	}
	return DevVisibleBrowserAgentPackageBinding{PackageID: pkg.PackageID, Runtime: bundle.ScriptManifest.Runtime, StageCount: len(plan.Stages)}, nil
}

func (m *devVisibleBrowserAgentManager) applyApprovedExecutionPolicy(ctx context.Context, session devVisibleBrowserAgentSession, pkg model.ClientExecutionPackage, waiver *BrowserAgentTestWaiver) error {
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		return err
	}
	selectors := append([]string{}, pkg.RecordingRunSpec.Redactions.MaskSelectors...)
	selectors = append(selectors, pkg.SafetyReport.RedactionSelectors...)
	allowedOrigins := append([]string{}, plan.ExplorationScope.AllowedOrigins...)
	if waiver != nil {
		// The unchanged App draft may contain scheme candidates discovered during
		// planning. The local waiver runtime narrows the actual Worker network
		// policy to the one real-page origin selected by the human login session.
		allowedOrigins = []string{waiver.AllowedOrigin}
	}
	return session.session.ApplyExecutionPolicy(ctx, driver.BrowserAgentWorkerExecutionPolicy{
		AllowedOrigins: allowedOrigins, AllowedRoutes: plan.ExplorationScope.AllowedRoutes,
		ForbiddenPages: plan.ForbiddenPages, ForbiddenPathPrefixes: plan.ExplorationScope.ForbiddenPathPrefixes,
		ForbiddenKeywords: plan.ExplorationScope.ForbiddenKeywords, MaskSelectors: uniqueStrings(selectors),
	})
}

func visibleExecutionResult(result model.RecordingResultPackage, eventDir string) *DevVisibleBrowserAgentResult {
	value := &DevVisibleBrowserAgentResult{ResultID: result.ResultID, StageEventPath: filepath.Join(eventDir, "browser-agent-stage-events.jsonl"), ValidationCount: len(result.ValidationReports), Artifacts: append([]model.ArtifactRef{}, result.GeneratedAssets...)}
	for _, artifact := range result.GeneratedAssets {
		if artifact.Kind == "demo_video" {
			value.VideoPath = artifact.URI
			break
		}
	}
	return value
}

func persistLocalVisibleResultPackage(recordingDir string, result model.RecordingResultPackage) (string, error) {
	if strings.TrimSpace(recordingDir) == "" {
		return "", errors.New("recording directory is required")
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(recordingDir, "recording-result-package.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (m *devVisibleBrowserAgentManager) view(ctx context.Context, sessionID string) (DevVisibleBrowserAgentView, error) {
	view, _, err := m.readySessionView(ctx, sessionID)
	return view, err
}

func (m *devVisibleBrowserAgentManager) readySessionView(ctx context.Context, sessionID string) (DevVisibleBrowserAgentView, devVisibleBrowserAgentSession, error) {
	m.mu.Lock()
	session, ok := m.sessions[strings.TrimSpace(sessionID)]
	m.mu.Unlock()
	if !ok {
		return DevVisibleBrowserAgentView{}, devVisibleBrowserAgentSession{}, errors.New("dev visible browser-agent session was not found")
	}
	if timeNowUTC().After(session.expiresAt) {
		_, _ = m.Abort(ctx, sessionID)
		return DevVisibleBrowserAgentView{DevTestOnly: true, SessionID: sessionID, Status: "expired", TargetOrigin: session.targetOrigin, Message: "本地测试会话已超时并关闭；请重新打开可见浏览器。"}, devVisibleBrowserAgentSession{}, nil
	}
	statusCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status, err := session.session.Status(statusCtx)
	if err != nil {
		return DevVisibleBrowserAgentView{}, devVisibleBrowserAgentSession{}, fmt.Errorf("inspect isolated visible browser: %w", err)
	}
	view := DevVisibleBrowserAgentView{DevTestOnly: true, SessionID: sessionID, TargetOrigin: session.targetOrigin, ActualURL: status.URL, PageTitle: status.Title, ExpiresAt: session.expiresAt}
	actual, parseErr := url.Parse(status.URL)
	if parseErr != nil || devVisibleOrigin(actual) != session.targetOrigin {
		view.Status = "source_mismatch"
		view.Message = "已停止在此状态执行：可见浏览器不在请求的真实本地产品来源上。"
		return view, session, nil
	}
	if actual.Path == "/login" || strings.HasPrefix(actual.Path, "/login/") {
		view.Status = "awaiting_manual_login"
		view.ManualAction = "请只在弹出的隔离浏览器窗口手动登录；登录后调用 continue。系统不会读取或保存密码、Cookie、Token、邮箱或手机号。"
		view.Message = "已确认访问真实本地来源，但当前仍在登录页；未执行任何业务动作。"
		return view, session, nil
	}
	view.Status = "ready_for_approved_package"
	view.Message = "已确认隔离浏览器位于真实目标页面。此本地测试接口只完成登录接力；后续仅可执行经协议校验和人工审批的执行包。"
	return view, session, nil
}

func validateDevVisiblePackageBinding(pkg model.ClientExecutionPackage, targetOrigin string) (DevVisibleBrowserAgentPackageBinding, error) {
	if err := model.ValidateClientExecutionPackageForCloudExecution(&pkg); err != nil {
		return DevVisibleBrowserAgentPackageBinding{}, fmt.Errorf("approved package protocol validation failed: %w", err)
	}
	if pkg.ApprovedAt.IsZero() || strings.TrimSpace(pkg.SafetyReport.HumanApproval.ApprovalID) == "" || strings.TrimSpace(pkg.SafetyReport.HumanApproval.PlanDigestSHA256) == "" {
		return DevVisibleBrowserAgentPackageBinding{}, errors.New("visible-session binding requires an App human-approved package")
	}
	bundle := pkg.ExecutableScriptBundle
	if bundle == nil || bundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		return DevVisibleBrowserAgentPackageBinding{}, errors.New("visible-session binding requires browser-agent-outline-v1")
	}
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		return DevVisibleBrowserAgentPackageBinding{}, fmt.Errorf("approved browser-agent plan compilation failed: %w", err)
	}
	baseURL, err := url.Parse(pkg.RecordingRunSpec.BaseURL)
	if err != nil || devVisibleOrigin(baseURL) != targetOrigin {
		return DevVisibleBrowserAgentPackageBinding{}, errors.New("approved package base_url does not match the visible real-page origin")
	}
	productURL, err := url.Parse(pkg.ProjectContextSummary.ProductURL)
	if err != nil || devVisibleOrigin(productURL) != targetOrigin {
		return DevVisibleBrowserAgentPackageBinding{}, errors.New("approved package product_url does not match the visible real-page origin")
	}
	if !containsExactString(plan.ExplorationScope.AllowedOrigins, targetOrigin) {
		return DevVisibleBrowserAgentPackageBinding{}, errors.New("approved package allowed_origins does not include the visible real-page origin")
	}
	if !visibleOriginsRestricted(plan.ExplorationScope.AllowedOrigins, targetOrigin) {
		return DevVisibleBrowserAgentPackageBinding{}, errors.New("approved package allowed_origins must be limited to the visible real-page origin")
	}
	if !containsExactString(pkg.RecordingRunSpec.AllowedDomains, baseURL.Hostname()) || !containsExactString(bundle.SecurityPolicy.AllowedDomains, baseURL.Hostname()) {
		return DevVisibleBrowserAgentPackageBinding{}, errors.New("approved package allowed_domains does not include the visible real-page host")
	}
	return DevVisibleBrowserAgentPackageBinding{PackageID: pkg.PackageID, Runtime: bundle.ScriptManifest.Runtime, StageCount: len(plan.Stages)}, nil
}

func visibleOriginsRestricted(origins []string, expected string) bool {
	for _, origin := range origins {
		if !strings.EqualFold(strings.TrimSpace(origin), strings.TrimSpace(expected)) {
			return false
		}
	}
	return true
}

func containsExactString(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(expected)) {
			return true
		}
	}
	return false
}

func (m *devVisibleBrowserAgentManager) take(sessionID string) (devVisibleBrowserAgentSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[strings.TrimSpace(sessionID)]
	if ok {
		delete(m.sessions, strings.TrimSpace(sessionID))
	}
	return session, ok
}

func (m *devVisibleBrowserAgentManager) closeSession(sessionID string, session devVisibleBrowserAgentSession) {
	_, _ = m.take(sessionID)
	closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := session.session.Close(closeCtx); err != nil {
		_ = session.session.Abort()
	}
}

func (m *devVisibleBrowserAgentManager) localOnlyGuard(ack bool) error {
	if m == nil || m.service == nil || m.service.runtime.Profile != config.ProfileDev || strings.EqualFold(m.service.runtime.Environment, "production") {
		return errors.New("dev visible browser-agent is available only in a local dev/test runtime")
	}
	if !ack {
		return errors.New("dev_test_ack=true is required for the local dev/test-only visible browser")
	}
	return nil
}

func devVisibleTargetURL(value string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(value))
	if err != nil || target.Scheme != "http" || target.Hostname() != "127.0.0.1" || target.Port() == "" {
		return nil, errors.New("target_url must be an explicit local http://127.0.0.1:<port>/ URL")
	}
	if target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return nil, errors.New("target_url must not include credentials, query parameters, or a fragment")
	}
	return target, nil
}

func devVisibleOrigin(value *url.URL) string {
	if value == nil {
		return ""
	}
	return value.Scheme + "://" + value.Host
}

func defaultDevVisibleMaskSelectors() []string {
	return []string{"input[type=password]", "input[type=email]", "input[type=tel]", "input[name*=token i]", "input[name*=key i]", "[data-sensitive]"}
}

func (s *DevHTTPServer) handleDevVisibleBrowserAgent(w http.ResponseWriter, r *http.Request) {
	manager := s.service.devVisibleBrowserAgent
	if manager == nil {
		writeBridgeValue(w, nil, errors.New("dev visible browser-agent is not configured"))
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/desktop/dev-visible-browser-agent/prepare" {
		var request DevVisibleBrowserAgentPrepareRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		view, err := manager.Prepare(r.Context(), request)
		writeBridgeValue(w, view, err)
		return
	}
	const prefix = "/v1/desktop/dev-visible-browser-agent/"
	remaining := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(strings.Trim(remaining, "/"), "/")
	if r.Method == http.MethodGet && len(parts) == 1 && strings.TrimSpace(parts[0]) != "" {
		view, err := manager.view(r.Context(), parts[0])
		writeBridgeValue(w, view, err)
		return
	}
	if len(parts) == 1 && parts[0] == "approved-package" && r.Method == http.MethodPost {
		var request DevVisibleBrowserAgentApprovedPackageRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		value, err := manager.BuildApprovedLocalTestPackage(r.Context(), request)
		writeBridgeValue(w, value, err)
		return
	}
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || (parts[1] != "continue" && parts[1] != "abort" && parts[1] != "bind-approved-package" && parts[1] != "execute-approved-package" && parts[1] != "execute-fixed-approved-package") || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var view DevVisibleBrowserAgentView
	var err error
	if parts[1] == "continue" {
		view, err = manager.Continue(r.Context(), parts[0])
	} else if parts[1] == "abort" {
		view, err = manager.Abort(r.Context(), parts[0])
	} else if parts[1] == "bind-approved-package" {
		var request DevVisibleBrowserAgentBindPackageRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		view, err = manager.BindApprovedPackage(r.Context(), parts[0], request)
	} else if parts[1] == "execute-fixed-approved-package" {
		var request DevVisibleBrowserAgentFixedExecuteRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		view, err = manager.ExecuteFixedApprovedLocalTestPackage(r.Context(), parts[0], request)
	} else {
		var request DevVisibleBrowserAgentExecuteRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		view, err = manager.ExecuteApprovedPackage(r.Context(), parts[0], request)
	}
	writeBridgeValue(w, view, err)
}
