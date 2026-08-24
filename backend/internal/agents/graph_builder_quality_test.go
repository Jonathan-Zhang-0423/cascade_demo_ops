package agents

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestGraphBuilderPrefersExecutableBusinessSelector(t *testing.T) {
	project := graphQualityProject()
	productMap := graphQualityProductMap(
		model.UIActionRef{ID: "generic", Label: "Open area", Kind: "click", Selector: "main"},
		model.UIActionRef{ID: "create", Label: "Create campaign", Kind: "click", Selector: "[data-testid='create-campaign']"},
	)

	graph, err := NewGraphBuilderAgent().GenerateGraph(context.Background(), project, productMap, graphQualityReport(project), graphQualityIntelligence(
		model.VerifiedInteractionAction{ID: "create", IntentGoalID: "intent_create", Label: "Create campaign", Kind: "click", Selector: "[data-testid='create-campaign']", IsBusiness: true, VerificationStatus: "verified", VerificationSource: "playwright_readonly_scan", SelectorScore: 100},
	))
	if err != nil {
		t.Fatal(err)
	}
	node := graphNodeBySelector(graph, "[data-testid='create-campaign']")
	if node == nil {
		t.Fatal("missing verified create node")
	}
	if node.ActionSpec == nil || node.ActionSpec.Type != model.GraphActionClick {
		t.Fatalf("expected executable click node, got %+v", node.ActionSpec)
	}
	if got := node.ActionSpec.Target.Selector; got != "[data-testid='create-campaign']" {
		t.Fatalf("expected data-testid selector, got %q", got)
	}
}

func TestFinalBuildCompletionValidationUsesEvidenceBoundResultAndLongPoll(t *testing.T) {
	now := time.Now().UTC()
	evidence := model.EvidenceRef{ID: "ev_build_result", Kind: model.EvidenceKindCodeSnapshot, Confidence: 0.9}
	candidate := model.SelectorCandidate{
		Kind: "testid", Value: "build-result-card", EvidenceID: evidence.ID, SourceKind: "source_scan",
		SourceDigest: "sha256:build-result", ObservedRole: "region", ObservedAccessibleName: "Build completed",
		ObservedAt: &now, EvidenceRefs: []model.EvidenceRef{evidence},
	}
	stage := model.BusinessStage{
		ID: "business_stage_final_observe", Kind: model.BusinessStageKindFinalObserve,
		UserIntent: "等待 Agent 真正编写完代码，直到全部步骤完成，最多 20 分钟。",
		EntryRoute: "/project/:id", ExpectedRouteAfterAction: "/project/:id",
		Action: model.BusinessActionSemantics{SuccessState: "Agent 构建完成结果可见"},
		Targets: []model.BusinessTargetCandidate{{
			ID: "intent_agent_build_complete", Label: "Agent 构建完成结果", Kind: "inspect",
			Selector: "[data-testid='build-result-card']", TestID: "build-result-card", SelectorScore: 100,
			EvidenceRefs: []model.EvidenceRef{evidence}, Alternatives: []model.SelectorCandidate{candidate},
		}},
	}
	validation := businessStageValidation(stage, model.GraphActionInspect, businessStageActionTarget(stage, "https://app.example.com"), true)
	if validation.Kind != "element_visible" || validation.Target.TestID != "build-result-card" || validation.TimeoutMS != maxBuildCompletionWaitMS {
		t.Fatalf("completion validation must poll the evidence-bound result, got %+v", validation)
	}
	if validation.Target.URL != "" {
		t.Fatalf("completion validation must not accept the build route by itself: %+v", validation.Target)
	}
}

func TestFinalObserveDoesNotReuseUnrelatedPageScanControlAsExecutionRoute(t *testing.T) {
	stage := model.BusinessStage{
		ID: "business_stage_final_observe", Kind: model.BusinessStageKindFinalObserve,
		EntryRoute: "/project/:id", ExpectedRouteAfterAction: "/project/:id",
		Action: model.BusinessActionSemantics{Label: "等待构建完成", SuccessState: "构建完成结果可见"},
		Targets: []model.BusinessTargetCandidate{{
			ID: "unrelated_project_idea", URL: "https://product.example/app", Kind: "inspect",
			Selector: "[data-testid='input-project-idea']", TestID: "input-project-idea",
		}},
	}
	target := businessStageActionTarget(stage, "https://product.example/app")
	if target.URL != "https://product.example/project/:id" {
		t.Fatalf("final observation must retain its stage entry route, got %+v", target)
	}
	if target.Selector != "" || target.TestID != "" {
		t.Fatalf("unrelated page-scan control must not become a final-observe action target: %+v", target)
	}
}

func TestSessionSetupValidationUsesPostLoginStateNotEmailActionTarget(t *testing.T) {
	stage := model.BusinessStage{
		ID: "business_stage_session_setup", Kind: model.BusinessStageKindSessionSetup,
		Title: "登录", Objective: "登录后进入工作台", ExpectedRouteAfterAction: "/app",
		Action: model.BusinessActionSemantics{SuccessState: "已进入工作台"},
	}
	target := model.ActionTarget{Selector: "[data-testid='input-email']", TestID: "input-email"}
	validation := businessStageValidation(stage, model.GraphActionFill, target, true)
	if validation.Kind != "url_matches" || validation.Target.URL != "/app" || validation.Target.Selector != "" || validation.Expected != "/app" {
		t.Fatalf("session validation reused the login/waitlist action target: %+v", validation)
	}
}

func TestSessionSetupGraphDoesNotInventAnUnscannedWorkspaceElement(t *testing.T) {
	stage := model.BusinessStage{
		ID: "business_stage_session_setup", Kind: model.BusinessStageKindSessionSetup,
		EntryRoute: "/login", ExpectedRouteAfterAction: "/app",
		Action: model.BusinessActionSemantics{SuccessState: "登录完成，页面进入工作台或目标业务页面。", NonDestructive: true},
	}
	node := graphNodeFromBusinessStage(&model.ProjectContext{}, stage, "https://product.example/login", "")
	if len(node.Validations) != 1 || node.Validations[0].Kind != "url_matches" || node.Validations[0].Target.URL != "/app" {
		t.Fatalf("session setup must keep only the evidence-bound post-login route validation: %+v", node.Validations)
	}
}

func TestSessionSetupActionTargetKeepsAuthenticationEntryRoute(t *testing.T) {
	stage := model.BusinessStage{
		ID:                       "business_stage_session_setup",
		Kind:                     model.BusinessStageKindSessionSetup,
		EntryRoute:               "/login",
		ExpectedRouteAfterAction: "/workspace",
		Targets: []model.BusinessTargetCandidate{{
			ID:         "verified_login_outcome",
			URL:        "/workspace",
			IsVerified: true,
		}},
	}
	target := businessStageActionTarget(stage, "https://product.example/login")
	if target.URL != "https://product.example/login" {
		t.Fatalf("session action target used post-login route: %+v", target)
	}
}

func TestModeSelectionValidationDoesNotReuseClickedControl(t *testing.T) {
	stage := model.BusinessStage{
		ID: "business_stage_select_build_mode", Kind: model.BusinessStageKindModeSelection,
		Title: "选择构建模式", Objective: "Agent 模式已选中",
		Action: model.BusinessActionSemantics{SuccessState: "Agent 模式已选中"},
	}
	validation := businessStageValidation(stage, model.GraphActionClick, model.ActionTarget{Selector: "[data-testid='mode-agent']"}, true)
	if validation.Kind != "page_changed" || validation.Target.Selector != "" || validation.Expected != true {
		t.Fatalf("mode validation reused the action target instead of the success state: %+v", validation)
	}
}

func TestGraphBuilderBlocksWhenOnlyGenericSelectorExists(t *testing.T) {
	project := graphQualityProject()
	productMap := graphQualityProductMap(
		model.UIActionRef{ID: "generic", Label: "Click main", Kind: "click", Selector: "main"},
	)

	_, err := NewGraphBuilderAgent().GenerateGraph(context.Background(), project, productMap, graphQualityReport(project), graphQualityIntelligence())
	if err == nil || !strings.Contains(err.Error(), "no verified business action") {
		t.Fatalf("generic-only evidence must block fake scripts, got %v", err)
	}
}

func TestGraphBuilderAvoidsLoginWhenBusinessActionExists(t *testing.T) {
	project := graphQualityProject()
	productMap := graphQualityProductMap(
		model.UIActionRef{ID: "login", Label: "Login", Kind: "click", Selector: "[data-testid='login-submit']"},
		model.UIActionRef{ID: "invite", Label: "Invite teammate", Kind: "click", Selector: "[data-testid='invite-user']"},
	)

	graph, err := NewGraphBuilderAgent().GenerateGraph(context.Background(), project, productMap, graphQualityReport(project), graphQualityIntelligence(
		model.VerifiedInteractionAction{ID: "login", IntentGoalID: "intent_login", Label: "Login", Kind: "click", Selector: "[data-testid='login-submit']", IsBusiness: false, VerificationStatus: "verified", VerificationSource: "playwright_readonly_scan", SelectorScore: 100},
		model.VerifiedInteractionAction{ID: "invite", IntentGoalID: "intent_invite", Label: "Invite teammate", Kind: "click", Selector: "[data-testid='invite-user']", IsBusiness: true, VerificationStatus: "verified", VerificationSource: "playwright_readonly_scan", SelectorScore: 100},
	))
	if err != nil {
		t.Fatal(err)
	}
	node := graphNodeBySelector(graph, "[data-testid='invite-user']")
	if node == nil || node.ActionSpec == nil {
		t.Fatalf("missing generated node: %+v", graph.Nodes)
	}
	if strings.Contains(strings.ToLower(node.Title+" "+node.ActionSpec.Target.Selector), "login") {
		t.Fatalf("primary value node should not repeat login when a business action exists: %+v", node)
	}
}

func TestGraphBuilderSkipsChromeToggleWhenBusinessActionExists(t *testing.T) {
	project := graphQualityProject()
	productMap := graphQualityProductMap(
		model.UIActionRef{ID: "sidebar", Label: "button sidebar toggle", Kind: "click", Selector: "[data-testid='button-sidebar-toggle']"},
		model.UIActionRef{ID: "new_project", Label: "New project", Kind: "click", Selector: "[data-testid='new-project']"},
	)

	graph, err := NewGraphBuilderAgent().GenerateGraph(context.Background(), project, productMap, graphQualityReport(project), graphQualityIntelligence(
		model.VerifiedInteractionAction{ID: "new_project", IntentGoalID: "intent_new_project", Label: "New project", Kind: "click", Selector: "[data-testid='new-project']", IsBusiness: true, VerificationStatus: "verified", VerificationSource: "playwright_readonly_scan", SelectorScore: 100},
	))
	if err != nil {
		t.Fatal(err)
	}
	node := graphNodeBySelector(graph, "[data-testid='new-project']")
	if node == nil || node.ActionSpec == nil {
		t.Fatalf("missing generated node: %+v", graph.Nodes)
	}
	if got := node.ActionSpec.Target.Selector; got != "[data-testid='new-project']" {
		t.Fatalf("expected business selector instead of chrome toggle, got %q", got)
	}
}

func TestGraphBuilderBlocksWhenOnlyChromeToggleExists(t *testing.T) {
	project := graphQualityProject()
	productMap := graphQualityProductMap(
		model.UIActionRef{ID: "sidebar", Label: "button sidebar toggle", Kind: "click", Selector: "[data-testid='button-sidebar-toggle']"},
	)

	_, err := NewGraphBuilderAgent().GenerateGraph(context.Background(), project, productMap, graphQualityReport(project), graphQualityIntelligence())
	if err == nil || !strings.Contains(err.Error(), "no verified business action") {
		t.Fatalf("chrome-only evidence must block fake scripts, got %v", err)
	}
}

func TestGraphBuilderUsesBusinessStagePlanAsPrimaryGraphSpine(t *testing.T) {
	project := graphQualityProject()
	project.DemoAccount = &model.DemoAccount{UsernameSecretRef: "credential://demo/test", PasswordSecretRef: "credential://demo/test"}
	runtimePageEvidence := []model.EvidenceRef{{ID: "ev_runtime_dashboard", Kind: model.EvidenceKindBrowserScan, Summary: "Browser Scan confirmed the current dashboard state"}}
	authObservedAt := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	authEvidence := []model.EvidenceRef{{ID: "ev_login_form", Kind: model.EvidenceKindBrowserScan, Summary: "Browser Scan confirmed the password-bearing authentication form"}}
	authCandidate := model.SelectorCandidate{
		Kind: "testid", Value: "login-password", EvidenceID: "ev_login_form", SourceKind: "page_scan", SourceDigest: "sha256:login-page",
		ObservedRole: "textbox", ObservedAccessibleName: "Password", ObservedURL: "https://app.example.com/login", ObservedRouteTemplate: "/login",
		ObservedPageRole: "authentication", ObservedFormRole: "authentication", EvidenceDigestSHA256: "sha256:login-page",
		ObservedAt: &authObservedAt, LastValidatedAt: authObservedAt, EvidenceRefs: authEvidence,
	}
	project.Inputs = &model.ProjectInputBundle{Requirements: []model.DemoRequirement{
		{ID: "requirement_project_name", Kind: "must_show", Description: "填写俄罗斯方块", Required: true},
		{ID: "requirement_observe_progress", Kind: "must_show", Description: "观察构建进度", Required: true},
	}}
	stagePlan := &model.BusinessStagePlan{
		ID:                     "business_stage_plan_test",
		ProjectID:              project.ID,
		SchemaVersion:          model.ProjectIntelligencePackSchemaVersion,
		CoreBusinessStageCount: 2,
		Stages: []model.BusinessStage{
			{
				ID:                       "business_stage_session_setup",
				Order:                    1,
				Kind:                     model.BusinessStageKindSessionSetup,
				Title:                    "登录",
				Objective:                "登录并进入工作台",
				RouteState:               model.BusinessRouteStateUnauthenticated,
				EntryRoute:               "/login",
				ExpectedRouteAfterAction: "/app",
				DurationMS:               7000,
				Action:                   model.BusinessActionSemantics{Type: string(model.GraphActionFill), Label: "登录", SuccessState: "进入工作台", NonDestructive: true},
				Targets:                  []model.BusinessTargetCandidate{{ID: "target_login_password", Label: "登录表单", Kind: "fill", Selector: "[data-testid='login-password']", TestID: "login-password", SelectorScore: 100, IsVerified: true, VerificationStatus: "verified", VerificationSource: "page_scan", EvidenceRefs: authEvidence, Alternatives: []model.SelectorCandidate{authCandidate}}},
				EvidenceRefs:             append(append([]model.EvidenceRef{}, runtimePageEvidence...), authEvidence...),
			},
			{
				ID:                       "business_stage_project_name_input",
				Order:                    2,
				Kind:                     model.BusinessStageKindBusinessInput,
				Title:                    "填写项目名称",
				Objective:                "填写俄罗斯方块项目名称",
				RouteState:               model.BusinessRouteStateCreationFlow,
				EntryRoute:               "/app",
				ExpectedRouteAfterAction: "/app",
				DurationMS:               13000,
				Action:                   model.BusinessActionSemantics{Type: string(model.GraphActionFill), Label: "项目名称", InputSemantic: "project_name", InputValue: "俄罗斯方块", SuccessState: "项目名称已填写", NonDestructive: true},
				Targets:                  []model.BusinessTargetCandidate{{ID: "target_project_name", Label: "项目名称", Kind: "fill", Selector: "[data-testid='project-name']", SelectorScore: 100}},
				EvidenceRefs:             []model.EvidenceRef{{ID: "ev_project_name", Kind: model.EvidenceKindSourceCode}},
			},
			{
				ID:                       "business_stage_observe_agent_progress",
				Order:                    3,
				Kind:                     model.BusinessStageKindObserveProgress,
				Title:                    "观察 agent 构建过程 45 秒",
				Objective:                "持续观察 agent 实际构建过程",
				RouteState:               model.BusinessRouteStateBuildRunning,
				EntryRoute:               "/project/:id",
				ExpectedRouteAfterAction: "/project/:id",
				DurationMS:               45000,
				Action:                   model.BusinessActionSemantics{Type: string(model.GraphActionWait), Label: "观察构建进度", SuccessState: "构建过程可见", NonDestructive: true},
				EvidenceRefs:             runtimePageEvidence,
			},
		},
	}
	intelligence := graphQualityIntelligence(
		model.VerifiedInteractionAction{ID: "unrelated_sidebar", Label: "button sidebar toggle", Kind: "click", Selector: "[data-testid='button-sidebar-toggle']", IsBusiness: true, VerificationStatus: "verified", SelectorScore: 100},
	)
	intelligence.BusinessStagePlan = stagePlan

	graph, err := NewGraphBuilderAgent().GenerateGraph(context.Background(), project, graphQualityProductMap(), graphQualityReport(project), intelligence)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != len(stagePlan.Stages) {
		t.Fatalf("graph should use one node per business stage, got %d want %d", len(graph.Nodes), len(stagePlan.Stages))
	}
	if graph.Nodes[1].ID != "business_stage_project_name_input" || graph.Nodes[1].ActionSpec.Type != model.GraphActionFill {
		t.Fatalf("business input stage did not become fill node: %+v", graph.Nodes[1])
	}
	if got := graph.Nodes[1].ActionSpec.Value; got != "俄罗斯方块" {
		t.Fatalf("project name semantic value lost: %q", got)
	}
	if got := graph.Nodes[1].Validations[0]; got.Kind != "value_equals" || got.Expected != "俄罗斯方块" {
		t.Fatalf("project input must verify the entered value, not only input visibility: %+v", got)
	}
	if graph.Nodes[2].ActionSpec.Type != model.GraphActionWait || graph.Nodes[2].DurationHintMS != 45000 {
		t.Fatalf("observe_progress must be wait-only and keep explicit duration: %+v", graph.Nodes[2])
	}
	if !graphNodeHasRequiredValidation(graph.Nodes[2]) {
		t.Fatal("an explicit observe_progress stage must retain a required result validation")
	}
	if len(graph.Requirements) != 2 || len(graph.Requirements[0].NodeRefs) != 1 || graph.Requirements[0].NodeRefs[0] != graph.Nodes[1].ID || len(graph.Requirements[0].EvidenceRefs) == 0 {
		t.Fatalf("project-name requirement was not bound to its validated input stage and evidence: %+v", graph.Requirements)
	}
	if len(graph.Requirements[1].NodeRefs) != 1 || graph.Requirements[1].NodeRefs[0] != graph.Nodes[2].ID || len(graph.Requirements[1].EvidenceRefs) == 0 {
		t.Fatalf("observe-progress requirement was not bound to its validated stage and evidence: %+v", graph.Requirements)
	}
	if graphNodeBySelector(graph, "[data-testid='button-sidebar-toggle']") != nil {
		t.Fatal("verified interaction selector pool should not override business stage spine")
	}

	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, graphQualityReport(project), graphQualityProductMap(), graph, intelligence)
	if err != nil {
		t.Fatal(err)
	}
	stage := stageApprovalByNodeID(pkg.ExecutableBundle, "business_stage_project_name_input")
	if stage == nil || stage.StageKind != model.BusinessStageKindBusinessInput || stage.RouteState != model.BusinessRouteStateCreationFlow {
		t.Fatalf("stage approval did not preserve business stage metadata: %+v", stage)
	}
	outline := outlineStageByNodeID(pkg.ExecutableBundle, "business_stage_observe_agent_progress")
	if outline == nil || outline.StageKind != model.BusinessStageKindObserveProgress || outline.DurationMS != 45000 {
		t.Fatalf("outline did not preserve observe_progress metadata: %+v", outline)
	}
}

func TestBindGraphRequirementsUsesStageSemanticsAndRejectsUnverifiedNodes(t *testing.T) {
	evidence := []model.EvidenceRef{{ID: "ev_runtime", Kind: model.EvidenceKindBrowserScan}}
	validatedNode := func(id string, kind model.BusinessStageKind) *model.GraphNode {
		return &model.GraphNode{
			ID: id, Title: string(kind), Metadata: map[string]any{"business_stage_kind": string(kind)},
			EvidenceRefs: evidence,
			Validations:  []model.ValidationSpec{{ID: "validate_" + id, Required: true, EvidenceRefs: evidence}},
		}
	}
	graph := &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{
		validatedNode("business_stage_session_setup", model.BusinessStageKindSessionSetup),
		validatedNode("business_stage_new_project_entry", model.BusinessStageKindBusinessAction),
		validatedNode("business_stage_project_name_input", model.BusinessStageKindBusinessInput),
		validatedNode("business_stage_start_build", model.BusinessStageKindBusinessSubmit),
		validatedNode("business_stage_observe_progress", model.BusinessStageKindObserveProgress),
		validatedNode("business_stage_final_observe", model.BusinessStageKindFinalObserve),
		validatedNode("business_stage_playable_preview", model.BusinessStageKindFinalObserve),
		validatedNode("business_stage_verify_playable_controls", model.BusinessStageKindFinalObserve),
		{ID: "business_stage_unverified_submit", Metadata: map[string]any{"business_stage_kind": string(model.BusinessStageKindBusinessSubmit)}, EvidenceRefs: evidence},
	}}
	project := &model.ProjectContext{Inputs: &model.ProjectInputBundle{Requirements: []model.DemoRequirement{
		{ID: "login", Kind: "must_show", Description: "登录成功", Required: true},
		{ID: "create", Kind: "must_show", Description: "新建项目并填写项目名称", Required: true},
		{ID: "start", Kind: "must_show", Description: "Agent 已启动", Required: true},
		{ID: "result", Kind: "must_show", Description: "最终实际运行效果", Required: true},
		{ID: "complete", Kind: "must_show", Description: "最多等待 20 分钟直到 Agent 构建完成", Required: true},
		{ID: "poll_complete", Kind: "must_show", Description: "持续轮询直到所有构建步骤完成或出现明确 build_complete", Required: true},
		{ID: "playable", Kind: "must_show", Description: "最终预览显示俄罗斯方块棋盘、得分和操作说明", Required: true},
		{ID: "keyboard", Kind: "must_show", Description: "用方向键实际试玩并确认方块位置发生变化", Required: true},
		{ID: "secret", Kind: "must_not_show", Description: "不得显示密码", Required: true},
	}}}

	bindGraphRequirements(project, graph)

	want := map[string]string{
		"login": "business_stage_session_setup", "create": "business_stage_new_project_entry",
		"start": "business_stage_start_build", "result": "business_stage_final_observe",
		"complete": "business_stage_final_observe", "playable": "business_stage_playable_preview",
		"poll_complete": "business_stage_final_observe",
		"keyboard":      "business_stage_verify_playable_controls",
	}
	for _, requirement := range graph.Requirements {
		if requirement.Kind != "must_show" {
			if len(requirement.NodeRefs) != 0 || requirement.Required {
				t.Fatalf("negative requirement must remain a safety constraint: %+v", requirement)
			}
			continue
		}
		if len(requirement.NodeRefs) != 1 || requirement.NodeRefs[0] != want[requirement.ID] || len(requirement.EvidenceRefs) == 0 {
			t.Fatalf("requirement %q mapped incorrectly: %+v", requirement.ID, requirement)
		}
	}
}

func TestBindGraphRequirementsDropsEmptyStaleRefsAndKeepsMissingEvidenceBlocked(t *testing.T) {
	graph := &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{{
		ID: "business_stage_start_build", Metadata: map[string]any{"business_stage_kind": string(model.BusinessStageKindBusinessSubmit)},
		Validations: []model.ValidationSpec{{ID: "validate_start", Required: true}},
	}}}
	project := &model.ProjectContext{Inputs: &model.ProjectInputBundle{Requirements: []model.DemoRequirement{{
		ID: "start", Kind: "must_show", Description: "Agent 已启动", Required: true,
	}}}}
	graph.Requirements = []model.GraphRequirement{{ID: "stale", NodeRefs: []string{"", "missing", "business_stage_start_build"}}}

	bindGraphRequirements(project, graph)

	if len(graph.Requirements) != 1 || len(graph.Requirements[0].NodeRefs) != 0 || len(graph.Requirements[0].EvidenceRefs) != 1 || graph.Requirements[0].EvidenceRefs[0].Kind != model.EvidenceKindUserInput {
		t.Fatalf("an unmapped node must stay blocked while retaining requirement provenance: %+v", graph.Requirements)
	}
}

func TestProjectIntelligenceTreatsDisplaySelectorsAsReadOnly(t *testing.T) {
	if got := actionKindFromSelector("input[aria-label*='项目']"); got != "fill" {
		t.Fatalf("expected aria-label input to be fillable, got %q", got)
	}
	if !selectorUsableForBusinessAction("input[aria-label*='项目']") {
		t.Fatal("aria-label input should be usable for business fill actions")
	}
	for _, selector := range []string{
		"[data-testid='user-email-display']",
		"[data-testid='user-name-display']",
		"[data-testid='current-user-avatar']",
	} {
		if got := actionKindFromSelector(selector); got != "inspect" {
			t.Fatalf("expected %s to be inspect-only, got %q", selector, got)
		}
		if selectorUsableForBusinessAction(selector) {
			t.Fatalf("display selector must not be usable for business action: %s", selector)
		}
	}
}

func TestIntentFallbackGeneratesTetrisBuildWorkflow(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "演示登录（7s），新建项目（13s，俄罗斯方块，构建模式），agent实际构建演示（45s等待）"
	project.DemoAccount = &model.DemoAccount{UsernameSecretRef: "local-dev/demo_username", PasswordSecretRef: "local-dev/demo_password"}
	intelligence := graphQualityIntelligence()
	intelligence.RunIntentScope = runIntentScopeForProject(project)
	intelligence.DemoIntent.Objective = project.ProductDescription
	intelligence.DemoIntent.Goals = []model.DemoIntentGoal{
		{ID: "intent_login", Label: "登录", Required: true},
		{ID: "intent_new_project", Label: "新建项目", Required: true, BusinessCritical: true, TargetKeywords: []string{"新建项目", "new project"}},
		{ID: "intent_tetris", Label: "俄罗斯方块", Required: true, BusinessCritical: true, TargetKeywords: []string{"俄罗斯方块", "tetris"}},
		{ID: "intent_build_mode", Label: "构建模式", Required: true, BusinessCritical: true, TargetKeywords: []string{"构建模式", "build mode"}},
		{ID: "intent_agent_build", Label: "agent实际构建演示", Required: true, BusinessCritical: true, TargetKeywords: []string{"agent", "构建", "生成"}},
	}
	plan := fallbackPlanFromExplicitIntent(project, intelligence, nil, errors.New("sidecar unavailable"))
	if plan == nil || plan.BusinessActionCount < 4 {
		t.Fatalf("expected explicit intent fallback business actions, got %+v", plan)
	}
	intelligence.VerifiedInteraction = plan

	graph, err := NewGraphBuilderAgent().GenerateGraph(context.Background(), project, graphQualityProductMap(), graphQualityReport(project), intelligence)
	if err != nil {
		t.Fatal(err)
	}
	projectNameNode := graphNodeByID(graph, "verified_intent_project_name")
	if projectNameNode == nil || projectNameNode.ActionSpec == nil || projectNameNode.ActionSpec.Type != model.GraphActionFill {
		t.Fatalf("project name stage must be a fill action, got %+v", projectNameNode)
	}
	pkg, err := NewScriptPackagerAgent().PackageScript(context.Background(), project, graphQualityReport(project), graphQualityProductMap(), graph, intelligence)
	if err != nil {
		t.Fatal(err)
	}
	outlineText := outlineAuditText(pkg.ExecutableBundle.StageApprovalPlan, pkg.ExecutableBundle.ScriptOutline)
	for _, want := range []string{"新建项目", "俄罗斯方块", "构建模式", "启动 agent 实际构建", "45000"} {
		if !strings.Contains(outlineText, want) {
			t.Fatalf("expected generated outline to contain %q:\n%s", want, outlineText)
		}
	}
	for _, forbidden := range []string{"user-email-display", "user-name-display", "button-regenerate-cancel", "button-confirm-rename", `fill(selector_intent_project_name, "",`} {
		if strings.Contains(outlineText, forbidden) {
			t.Fatalf("generated outline must not contain %q:\n%s", forbidden, outlineText)
		}
	}
	markdown := pkg.ExecutableBundle.ApprovalMarkdown.InlineMarkdown
	for _, forbidden := range []string{"graph can be approved", "排练通过率", "rehearsal pass rate"} {
		if strings.Contains(markdown, forbidden) {
			t.Fatalf("approval markdown must not add unrequested goal %q:\n%s", forbidden, markdown)
		}
	}
}

func TestIntentFallbackRejectsMismatchedAndNegativeCodeCandidates(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "新建项目俄罗斯方块，构建模式，agent实际构建演示45秒"
	intelligence := graphQualityIntelligence()
	intelligence.RunIntentScope = runIntentScopeForProject(project)
	intelligence.DemoIntent.Objective = project.ProductDescription
	candidates := []model.InteractionProbe{
		{ID: "bad_name", Label: "确认重命名", Kind: "click", Selector: "[data-testid='button-confirm-rename']", Source: "code_reader", IsBusiness: true, SelectorScore: 100},
		{ID: "bad_cancel", Label: "取消重新生成", Kind: "click", Selector: "[data-testid='button-regenerate-cancel']", Source: "code_reader", IsBusiness: true, SelectorScore: 100},
	}
	plan := fallbackPlanFromExplicitIntent(project, intelligence, candidates, errors.New("page scan unavailable"))
	if plan == nil {
		t.Fatal("expected runtime-adaptive plan")
	}
	text := ""
	for _, action := range plan.Actions {
		text += action.Label + " " + action.Selector + "\n"
	}
	for _, forbidden := range []string{"button-confirm-rename", "button-regenerate-cancel"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("fallback must not bind mismatched/negative selector %q:\n%s", forbidden, text)
		}
	}
	for _, action := range plan.Actions {
		if action.Kind != "wait" && action.Selector != "" {
			t.Fatalf("runtime-adaptive fallback must preserve semantic intent without a guessed primary selector: %+v", action)
		}
		if !action.NonDestructive {
			t.Fatalf("allowlisted fallback action lost its non-destructive authority: %+v", action)
		}
		if containsString(action.WaitConditions, "networkidle") {
			t.Fatalf("runtime-adaptive fallback must not add redundant networkidle waits: %+v", action.WaitConditions)
		}
	}
}

func TestBusinessStageWaitUntilDoesNotDefaultToNetworkIdle(t *testing.T) {
	if got := businessStageWaitUntil(model.BusinessStage{}); got != "domcontentloaded" {
		t.Fatalf("default wait_until=%q, want domcontentloaded", got)
	}
	if got := businessStageWaitUntil(model.BusinessStage{Action: model.BusinessActionSemantics{WaitConditions: []string{"networkidle"}}}); got != "networkidle" {
		t.Fatalf("explicit approved networkidle must be preserved, got %q", got)
	}
}

func TestProductMapAndDossierFilterUnsafeIntentEvidence(t *testing.T) {
	project := graphQualityProject()
	project.ProductDescription = "新建项目俄罗斯方块，构建模式，agent实际构建演示45秒"
	intelligence := &model.ProjectIntelligencePack{
		DemoIntent: &model.DemoIntentSpec{
			Objective: "新建项目俄罗斯方块，构建模式，agent实际构建演示45秒，并展示 graph can be approved",
		},
		InteractionSurfaces: []model.InteractionSurface{{
			ID:     "surface_dashboard",
			PageID: "page_dashboard",
			URL:    "https://app.example.com/dashboard",
			Actions: []model.UIActionRef{
				{ID: "new", Label: "button new project", Kind: "click", Selector: "[data-testid='button-new-project']"},
				{ID: "cancel", Label: "button regenerate cancel", Kind: "click", Selector: "[data-testid='button-regenerate-cancel']"},
				{ID: "rename", Label: "button confirm rename", Kind: "click", Selector: "[data-testid='button-confirm-rename']"},
				{ID: "approve", Label: "button approve all", Kind: "click", Selector: "[data-testid='button-approve-all']"},
			},
		}},
	}
	pages := productPagesFromIntelligence(project, intelligence, nil, "https://app.example.com")
	if len(pages) != 1 || len(pages[0].PrimaryActions) != 1 {
		t.Fatalf("expected only safe primary action, got %+v", pages)
	}
	if got := pages[0].PrimaryActions[0].Selector; got != "[data-testid='button-new-project']" {
		t.Fatalf("unexpected retained action: %s", got)
	}
	summary := dossierEvidenceSummary([]string{"[data-testid='button-new-project'], [data-testid='button-regenerate-cancel'], [data-testid='button-confirm-rename'], [data-testid='button-approve-all']"}, projectIntentText(project, intelligence))
	if strings.Contains(summary, "button-regenerate-cancel") || strings.Contains(summary, "button-confirm-rename") || strings.Contains(summary, "button-approve-all") {
		t.Fatalf("dossier summary retained unsafe token: %s", summary)
	}
	if !strings.Contains(summary, "button-new-project") {
		t.Fatalf("dossier summary dropped safe token: %s", summary)
	}
}

func TestForbiddenRequirementsDoNotCountAsPositiveCoverage(t *testing.T) {
	project := graphQualityProject()
	project.MustShow = []string{"新建项目"}
	project.ForbiddenPages = []string{"/billing"}
	project.ForbiddenData = []string{"api_key"}
	requirements := requirementsFromProject(project)
	for _, requirement := range requirements {
		if requirement.Kind == "forbidden_page" || requirement.Kind == "forbidden_data" {
			if requirement.Required {
				t.Fatalf("safety requirement must not count toward positive coverage: %+v", requirement)
			}
		}
	}
}

func graphQualityProject() *model.ProjectContext {
	return &model.ProjectContext{
		ID:             "project_graph_quality",
		SchemaVersion:  model.ProjectContextSchemaVersion,
		Mode:           model.AppModeDesktop,
		ProductURL:     "https://app.example.com/dashboard",
		TargetAudience: "product team",
		AccessPolicy:   &model.AccessPolicy{AllowedDomains: []string{"app.example.com"}},
	}
}

func graphQualityReport(project *model.ProjectContext) *model.MultimodalUnderstandingReport {
	return &model.MultimodalUnderstandingReport{
		ID:            "report_graph_quality",
		ProjectID:     project.ID,
		SchemaVersion: model.MultimodalUnderstandingReportSchemaVersion,
		Summary:       "quality fixture",
	}
}

func graphQualityProductMap(actions ...model.UIActionRef) *model.ProductMap {
	return &model.ProductMap{
		ID:        "map_graph_quality",
		ProjectID: "project_graph_quality",
		Version:   1,
		Summary:   "quality fixture",
		Pages: []*model.ProductPage{{
			ID:             "page_dashboard",
			URL:            "https://app.example.com/dashboard",
			Title:          "Dashboard",
			Purpose:        "Show the main workflow.",
			PrimaryActions: actions,
		}},
		Features: []*model.Feature{{
			ID:        "feature_campaign",
			Name:      "Campaign workflow",
			UserValue: "Create and inspect a campaign workflow.",
		}},
	}
}

func graphNodeByID(graph *model.DemoWorkflowGraph, id string) *model.GraphNode {
	if graph == nil {
		return nil
	}
	for _, node := range graph.Nodes {
		if node != nil && node.ID == id {
			return node
		}
	}
	return nil
}

func graphNodeBySelector(graph *model.DemoWorkflowGraph, selector string) *model.GraphNode {
	if graph == nil {
		return nil
	}
	for _, node := range graph.Nodes {
		if node != nil && node.ActionSpec != nil && node.ActionSpec.Target.Selector == selector {
			return node
		}
	}
	return nil
}

func graphHasRuntimeAdaptiveBusinessNode(graph *model.DemoWorkflowGraph) bool {
	if graph == nil {
		return false
	}
	for _, node := range graph.Nodes {
		if node == nil || node.ActionSpec == nil || !isBusinessAction(node.ActionSpec.Type) {
			continue
		}
		if node.Metadata != nil && node.Metadata["runtime_adaptive"] == true {
			return true
		}
	}
	return false
}

func graphQualityIntelligence(actions ...model.VerifiedInteractionAction) *model.ProjectIntelligencePack {
	businessCount := 0
	for i := range actions {
		if actions[i].DurationHintMS == 0 {
			actions[i].DurationHintMS = 12000
		}
		if actions[i].VerificationStatus == "" {
			actions[i].VerificationStatus = "verified"
		}
		if actions[i].IsBusiness {
			businessCount++
		}
	}
	return &model.ProjectIntelligencePack{
		ID:            "intel_graph_quality",
		ProjectID:     "project_graph_quality",
		SchemaVersion: model.ProjectIntelligencePackSchemaVersion,
		DemoIntent: &model.DemoIntentSpec{
			ID:        "intent_graph_quality",
			ProjectID: "project_graph_quality",
			Goals:     []model.DemoIntentGoal{{ID: "intent_create", Label: "Create campaign", BusinessCritical: true, Required: true}},
		},
		VerifiedInteraction: &model.VerifiedInteractionPlan{
			ID:                  "verified_graph_quality",
			ProjectID:           "project_graph_quality",
			IntentID:            "intent_graph_quality",
			SchemaVersion:       model.ProjectIntelligencePackSchemaVersion,
			Actions:             actions,
			BusinessActionCount: businessCount,
			VerificationMode:    "unit_test",
		},
	}
}
