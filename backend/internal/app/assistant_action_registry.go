package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"cascade-demoops/backend/internal/model"
)

const assistantActionModeV2 = "agent_actions_v2"

var assistantActionRegistry = []model.AgentActionSpecification{
	{ID: "configuration.apply_patch", Title: "应用配置变更", InputSchema: assistantActionInputSchema("configuration.apply_patch"), AllowedPhases: []string{"configuration"}, Risk: model.AgentActionRiskReadOnly, AutoPolicy: "automatic", Idempotent: true},
	{ID: "source.select_local", Title: "选择本地项目", InputSchema: assistantActionInputSchema("source.select_local"), AllowedPhases: []string{"configuration"}, Risk: model.AgentActionRiskReadOnly, AutoPolicy: "user_gesture", Idempotent: true, RequiresUserGesture: true, OutputKinds: []string{"source_ref"}},
	{ID: "source.connect_github", Title: "连接 GitHub", InputSchema: assistantActionInputSchema("source.connect_github"), AllowedPhases: []string{"configuration"}, Risk: model.AgentActionRiskReadOnly, AutoPolicy: "user_gesture", Idempotent: true, RequiresUserGesture: true, OutputKinds: []string{"source_ref"}},
	{ID: "source.attach_requirement", Title: "添加需求文档", InputSchema: assistantActionInputSchema("source.attach_requirement"), AllowedPhases: []string{"configuration"}, Risk: model.AgentActionRiskReadOnly, AutoPolicy: "user_gesture", Idempotent: true, RequiresUserGesture: true, OutputKinds: []string{"source_ref"}},
	{ID: "source.attach_brand_asset", Title: "添加品牌素材", InputSchema: assistantActionInputSchema("source.attach_brand_asset"), AllowedPhases: []string{"configuration"}, Risk: model.AgentActionRiskReadOnly, AutoPolicy: "user_gesture", Idempotent: true, RequiresUserGesture: true, OutputKinds: []string{"source_ref"}},
	{ID: "credential.store_demo", Title: "安全保存演示账号", InputSchema: assistantActionInputSchema("credential.store_demo"), AllowedPhases: []string{"configuration"}, Risk: model.AgentActionRiskSessionWrite, AutoPolicy: "user_gesture", Idempotent: true, RequiresUserGesture: true, OutputKinds: []string{"credential_ref"}},
	{ID: "analysis.run_local", Title: "运行本地分析", InputSchema: assistantActionInputSchema("analysis.run_local"), AllowedPhases: []string{"configuration", "local_analysis"}, Risk: model.AgentActionRiskReadOnly, AutoPolicy: "automatic_when_ready", Idempotent: true, OutputKinds: []string{"analysis_project_ref"}},
	{ID: "workstation.open", Title: "打开工作台", InputSchema: assistantActionInputSchema("workstation.open"), AllowedPhases: []string{"local_analysis", "approval", "execution", "result_review"}, Risk: model.AgentActionRiskReadOnly, AutoPolicy: "automatic", Idempotent: true},
	{ID: "source.continue_page_only", Title: "仅使用网页证据继续", InputSchema: assistantActionInputSchema("source.continue_page_only"), AllowedPhases: []string{"local_analysis"}, Risk: model.AgentActionRiskReadOnly, AutoPolicy: "confirm", Idempotent: true},
	{ID: "execution.approve_upload", Title: "审批并上传执行包", InputSchema: assistantActionInputSchema("execution.approve_upload"), AllowedPhases: []string{"approval"}, Risk: model.AgentActionRiskExternalWrite, AutoPolicy: "confirm_digest", Idempotent: true, RequiresUserGesture: true},
	{ID: "result.download_verify", Title: "下载并校验成品", InputSchema: assistantActionInputSchema("result.download_verify"), AllowedPhases: []string{"result_review"}, Risk: model.AgentActionRiskReadOnly, AutoPolicy: "automatic", Idempotent: true},
	{ID: "result.review", Title: "审核成品", InputSchema: assistantActionInputSchema("result.review"), AllowedPhases: []string{"result_review"}, Risk: model.AgentActionRiskExternalWrite, AutoPolicy: "user_gesture", Idempotent: true, RequiresUserGesture: true},
	{ID: "repair.retry_page_scan", Title: "重新扫描登录页", InputSchema: assistantActionInputSchema("repair.retry_page_scan"), AllowedPhases: []string{"local_analysis", "repair"}, Risk: model.AgentActionRiskSessionWrite, AutoPolicy: "confirm", Idempotent: true},
	{ID: "repair.regenerate_package", Title: "重新生成执行包", InputSchema: assistantActionInputSchema("repair.regenerate_package"), AllowedPhases: []string{"local_analysis", "repair"}, Risk: model.AgentActionRiskReadOnly, AutoPolicy: "automatic", Idempotent: true},
}

func assistantActionInputSchema(actionID string) map[string]any {
	properties := map[string]any{}
	required := []string{}
	switch actionID {
	case "configuration.apply_patch":
		stringList := map[string]any{"type": "array", "items": map[string]any{"type": "string", "maxLength": 500}, "maxItems": 100}
		properties["patch"] = map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"projectName":       map[string]any{"type": "string", "maxLength": 160},
				"productURL":        map[string]any{"type": "string", "pattern": "^https://"},
				"objective":         map[string]any{"type": "string", "maxLength": 2000},
				"targetAudience":    map[string]any{"type": "string", "maxLength": 500},
				"targetDurationSec": map[string]any{"type": "integer", "minimum": 5, "maximum": 600},
				"mustShow":          stringList,
				"mustNotShow":       stringList,
				"forbiddenPages":    stringList,
				"forbiddenData":     stringList,
				"allowedDomains":    stringList,
				"brandTone":         map[string]any{"type": "string", "maxLength": 500},
			},
		}
		required = append(required, "patch")
	case "source.select_local", "source.connect_github", "source.attach_requirement", "source.attach_brand_asset":
		properties["selectedSources"] = map[string]any{
			"type": "array", "minItems": 1,
			"items": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"ref":   map[string]any{"type": "string", "pattern": "^source_"},
					"kind":  map[string]any{"type": "string", "enum": []string{"local_repository", "github_repository", "requirement_document", "brand_asset"}},
					"label": map[string]any{"type": "string", "maxLength": 160},
					"url":   map[string]any{"type": "string", "pattern": "^https://github\\.com/"},
				},
				"required": []string{"kind", "label"},
			},
		}
		required = append(required, "selectedSources")
	case "credential.store_demo":
		properties["credentialRefs"] = map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "pattern": "^credential://demo/"}}
		required = append(required, "credentialRefs")
	case "workstation.open":
		properties["workstation"] = map[string]any{"type": "string", "enum": []string{"overview", "evidence", "plan", "approval", "execution", "repair", "assets", "editor"}}
		required = append(required, "workstation")
	case "source.continue_page_only":
		properties["assessmentHash"] = map[string]any{"type": "string", "pattern": "^sha256:"}
		required = append(required, "assessmentHash")
	case "execution.approve_upload":
		properties["approvalSubjectDigest"] = map[string]any{"type": "string", "pattern": "^sha256:"}
		properties["riskConfirmed"] = map[string]any{"type": "boolean", "const": true}
		required = append(required, "approvalSubjectDigest", "riskConfirmed")
	case "result.review":
		properties["decision"] = map[string]any{"type": "string", "enum": []string{"approved", "re_edit", "re_record"}}
		required = append(required, "decision")
	}
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func agentActionsV2Enabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("CASCADE_AGENT_ACTIONS_V2")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func assistantActionsAvailableForStage(stage string) []model.AgentActionSpecification {
	phase := map[string]string{
		"configuration":         "configuration",
		"local_analysis":        "local_analysis",
		"plan_review":           "local_analysis",
		"source_binding_review": "local_analysis",
		"package_approval":      "approval",
		"server_execution":      "execution",
		"result_review":         "result_review",
		"repair":                "repair",
	}[stage]
	if phase == "" {
		return nil
	}
	result := make([]model.AgentActionSpecification, 0, len(assistantActionRegistry))
	for _, spec := range assistantActionRegistry {
		for _, allowed := range spec.AllowedPhases {
			if allowed == phase {
				result = append(result, spec)
				break
			}
		}
	}
	return result
}

func assistantActionSpecForProposal(kind model.AssistantProposalKind) (model.AgentActionSpecification, bool) {
	id := map[model.AssistantProposalKind]string{
		model.AssistantProposalConfigurationPatch:          "configuration.apply_patch",
		model.AssistantProposalSelectProjectSource:         "source.select_local",
		model.AssistantProposalSelectLocalProject:          "source.select_local",
		model.AssistantProposalConnectGitHub:               "source.connect_github",
		model.AssistantProposalAttachRequirementDocument:   "source.attach_requirement",
		model.AssistantProposalAttachBrandAsset:            "source.attach_brand_asset",
		model.AssistantProposalStoreDemoCredential:         "credential.store_demo",
		model.AssistantProposalConfirmConfiguration:        "analysis.run_local",
		model.AssistantProposalStartLocalAnalysis:          "analysis.run_local",
		model.AssistantProposalOpenWorkstation:             "workstation.open",
		model.AssistantProposalContinueWithWebpageEvidence: "source.continue_page_only",
		model.AssistantProposalRetryPageScan:               "repair.retry_page_scan",
		model.AssistantProposalRegenerateExecutionPackage:  "repair.regenerate_package",
	}[kind]
	for _, spec := range assistantActionRegistry {
		if spec.ID == id {
			return spec, true
		}
	}
	return model.AgentActionSpecification{}, false
}

func refreshAssistantActionState(session *model.AssistantSession) {
	if session == nil {
		return
	}
	if session.ActionMode == "" && agentActionsV2Enabled() {
		session.ActionMode = assistantActionModeV2
	}
	session.IntentPlan = agentIntentPlanFromDraft(session.Configuration)
	if session.ActionMode != assistantActionModeV2 {
		return
	}
	session.ActionCatalog = append([]model.AgentActionSpecification(nil), assistantActionRegistry...)
	existing := map[string]model.AgentAction{}
	for _, action := range session.Actions {
		existing[action.ProposalID] = action
	}
	actions := make([]model.AgentAction, 0)
	batches := make([]model.AgentActionBatch, 0)
	for messageIndex := range session.Messages {
		actionIDs := make([]string, 0)
		for proposalIndex := range session.Messages[messageIndex].Proposals {
			proposal := session.Messages[messageIndex].Proposals[proposalIndex]
			spec, ok := assistantActionSpecForProposal(proposal.Kind)
			if !ok {
				continue
			}
			action := existing[proposal.ID]
			if action.ID == "" {
				action.ID = "action_" + strings.TrimPrefix(proposal.ID, "proposal_")
				action.IdempotencyKey = action.ID
			}
			action.SpecID = spec.ID
			action.Title = proposal.Title
			action.Description = proposal.Description
			action.Risk = spec.Risk
			action.Status = proposal.Status
			action.AutoExecutable = spec.AutoPolicy == "automatic" || spec.AutoPolicy == "automatic_when_ready"
			action.RequiresUserGesture = spec.RequiresUserGesture
			action.DependsOn = actionDependencies(proposal.Kind)
			action.DependencyDigest = assistantDependencyDigest(session.Configuration, action.DependsOn)
			action.ProposalID = proposal.ID
			action.TargetWorkstation = proposal.TargetWorkstation
			action.ExecutionResult = proposal.ExecutionResult
			actions = append(actions, action)
			actionIDs = append(actionIDs, action.ID)
		}
		if len(actionIDs) > 0 {
			batchID := "batch_" + shortAssistantDigest(strings.Join(actionIDs, "|"))
			for index := range actions {
				for _, id := range actionIDs {
					if actions[index].ID == id {
						actions[index].BatchID = batchID
					}
				}
			}
			batches = append(batches, model.AgentActionBatch{ID: batchID, ActionIDs: actionIDs, BaseIntentDigest: session.IntentPlan.Digest, Status: batchStatus(actions, actionIDs), RequiresConfirmation: batchRequiresConfirmation(actions, actionIDs), IdempotencyKey: batchID})
		}
	}
	session.Actions = actions
	session.ActionBatches = batches
}

func agentIntentPlanFromDraft(draft model.ProjectConfigurationDraft) *model.AgentIntentPlan {
	plan := &model.AgentIntentPlan{SchemaVersion: model.AgentIntentPlanSchemaVersion, Objective: draft.Objective, Version: draft.Version, Entities: map[string]string{}, Constraints: cleanStrings(append(append([]string{}, draft.MustNotShow...), draft.ForbiddenPages...))}
	if draft.ProjectName != "" {
		plan.Entities["project_name"] = draft.ProjectName
	}
	if draft.ProductURL != "" {
		plan.Entities["product_url"] = draft.ProductURL
	}
	steps := append([]string(nil), draft.MustShow...)
	if len(steps) == 0 && strings.TrimSpace(draft.Objective) != "" {
		steps = []string{draft.Objective}
	}
	for index, text := range steps {
		risk := intentStepRisk(text)
		plan.Steps = append(plan.Steps, model.AgentIntentStep{ID: fmt.Sprintf("intent_step_%02d", index+1), Order: index + 1, Action: text, Risk: risk, ExpectedOutcome: expectedOutcomeForIntentStep(text), EvidenceRequirement: "deterministic_url_or_element_assertion"})
	}
	plan.RequiredCapabilities = []string{"local_analysis", "page_scan", "execution_package"}
	plan.MissingRequirements = append([]string(nil), draft.MissingFields...)
	if assistantIntentRequiresLogin(draft) && len(draft.CredentialRefs) == 0 && !assistantIntentHasManualLoginCheckpoint(draft) {
		plan.MissingRequirements = append(plan.MissingRequirements, "credentialRefs_or_manualLoginCheckpoint")
		plan.RequiredCapabilities = append(plan.RequiredCapabilities, "credential_ref")
	}
	plan.MissingRequirements = cleanStrings(plan.MissingRequirements)
	if len(plan.MissingRequirements) == 0 {
		plan.Readiness = "ready"
	} else {
		plan.Readiness = "incomplete"
	}
	copy := *plan
	copy.Digest = ""
	encoded, _ := json.Marshal(copy)
	digest := sha256.Sum256(encoded)
	plan.Digest = "sha256:" + hex.EncodeToString(digest[:])
	return plan
}

func assistantIntentRequiresLogin(draft model.ProjectConfigurationDraft) bool {
	text := strings.ToLower(strings.Join(append([]string{draft.Objective}, draft.MustShow...), " "))
	return containsAnyAssistantText(text, "登录", "登陆", "登入", "sign in", "log in", "login")
}

func assistantIntentHasManualLoginCheckpoint(draft model.ProjectConfigurationDraft) bool {
	text := strings.ToLower(strings.Join(append(append([]string{}, draft.MustShow...), draft.MustNotShow...), " "))
	return containsAnyAssistantText(text, "人工登录", "手动登录", "manual login", "human checkpoint")
}

func intentStepRisk(value string) model.AgentActionRisk {
	text := strings.ToLower(value)
	if containsAnyAssistantText(text, "删除", "支付", "付款", "delete", "remove", "pay") {
		return model.AgentActionRiskDestructive
	}
	if containsAnyAssistantText(text, "新建", "创建", "提交", "保存", "生成", "create", "submit", "save", "generate") {
		return model.AgentActionRiskExternalWrite
	}
	if containsAnyAssistantText(text, "登录", "登陆", "login", "sign in") {
		return model.AgentActionRiskSessionWrite
	}
	return model.AgentActionRiskReadOnly
}

func expectedOutcomeForIntentStep(value string) string {
	if intentStepRisk(value) == model.AgentActionRiskSessionWrite {
		return "已进入经验证的工作台页面"
	}
	return "页面出现与该业务步骤对应的可验证结果"
}

func actionDependencies(kind model.AssistantProposalKind) []string {
	switch kind {
	case model.AssistantProposalSelectProjectSource, model.AssistantProposalSelectLocalProject, model.AssistantProposalConnectGitHub:
		return []string{"sources"}
	case model.AssistantProposalStoreDemoCredential:
		return []string{"credentialRefs"}
	case model.AssistantProposalConfirmConfiguration, model.AssistantProposalStartLocalAnalysis:
		return []string{"projectName", "productURL", "objective", "targetAudience", "sources", "credentialRefs"}
	default:
		return []string{"configuration"}
	}
}

func assistantDependencyDigest(draft model.ProjectConfigurationDraft, dependencies []string) string {
	values := map[string]any{}
	for _, dependency := range dependencies {
		switch dependency {
		case "sources":
			values[dependency] = draft.Sources
		case "credentialRefs":
			values[dependency] = draft.CredentialRefs
		case "projectName":
			values[dependency] = draft.ProjectName
		case "productURL":
			values[dependency] = draft.ProductURL
		case "objective":
			values[dependency] = draft.Objective
		case "targetAudience":
			values[dependency] = draft.TargetAudience
		default:
			values[dependency] = draft.Hash
		}
	}
	encoded, _ := json.Marshal(values)
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func batchStatus(actions []model.AgentAction, ids []string) string {
	status := "completed"
	for _, action := range actions {
		if containsAssistantActionID(ids, action.ID) && action.Status == "available" {
			return "available"
		}
		if containsAssistantActionID(ids, action.ID) && action.Status == "dismissed" {
			status = "partial"
		}
	}
	return status
}

func batchRequiresConfirmation(actions []model.AgentAction, ids []string) bool {
	for _, action := range actions {
		if containsAssistantActionID(ids, action.ID) && (action.RequiresUserGesture || action.Risk == model.AgentActionRiskExternalWrite || action.Risk == model.AgentActionRiskDestructive) {
			return true
		}
	}
	return false
}

func containsAssistantActionID(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func shortAssistantDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:8])
}

func (s *Service) CompleteAssistantAction(ctx context.Context, sessionID, actionID string, request model.AgentActionCompleteRequest) (*model.AssistantSession, error) {
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return nil, errors.New("idempotency key is required")
	}
	if strings.TrimSpace(request.DependencyDigest) == "" {
		return nil, errors.New("assistant action dependency digest is required")
	}
	session, err := s.GetAssistantSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	refreshAssistantActionState(session)
	var selected *model.AgentAction
	for index := range session.Actions {
		if session.Actions[index].ID == actionID {
			selected = &session.Actions[index]
			break
		}
	}
	if selected == nil || selected.ProposalID == "" {
		return nil, errors.New("assistant action not found")
	}
	if request.DependencyDigest != selected.DependencyDigest {
		return nil, errors.New("assistant action dependency is stale")
	}
	return s.CompleteAssistantClientAction(ctx, sessionID, selected.ProposalID, model.AssistantClientActionResultRequest{BaseVersion: session.Configuration.Version, IdempotencyKey: request.IdempotencyKey, SelectedSources: request.SelectedSources, CredentialRefs: request.CredentialRefs})
}

func (s *Service) ConfirmAssistantActionBatch(ctx context.Context, sessionID, batchID string, request model.AgentActionBatchConfirmRequest) (*model.AssistantSession, error) {
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return nil, errors.New("idempotency key is required")
	}
	session, err := s.GetAssistantSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	refreshAssistantActionState(session)
	if session.IntentPlan == nil || request.BaseIntentDigest != session.IntentPlan.Digest {
		return nil, errors.New("assistant intent plan is stale")
	}
	var batch *model.AgentActionBatch
	for index := range session.ActionBatches {
		if session.ActionBatches[index].ID == batchID {
			batch = &session.ActionBatches[index]
			break
		}
	}
	if batch == nil {
		return nil, errors.New("assistant action batch not found")
	}
	if batch.ApprovalSubjectDigest != "" && request.ApprovalSubjectDigest != batch.ApprovalSubjectDigest {
		return nil, errors.New("assistant action batch approval subject is stale")
	}
	ids := append([]string(nil), batch.ActionIDs...)
	sort.Strings(ids)
	for _, id := range ids {
		for _, action := range session.Actions {
			if action.ID != id || action.Status != "available" || action.ProposalID == "" || action.RequiresUserGesture {
				continue
			}
			session, err = s.ConfirmAssistantProposal(ctx, sessionID, action.ProposalID, model.AssistantProposalDecisionRequest{BaseVersion: session.Configuration.Version, IdempotencyKey: request.IdempotencyKey + ":" + id})
			if err != nil {
				return nil, err
			}
		}
	}
	return session, nil
}
