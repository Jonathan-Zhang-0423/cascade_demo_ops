package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/credentialstore"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

var (
	assistantSecretPattern      = regexp.MustCompile(`(?i)(password|token|secret|api[_-]?key)\s*[:=]\s*[^\s,;]+`)
	windowsAbsolutePathPattern  = regexp.MustCompile(`(?i)\b[A-Z]:\\(?:[^\s<>:"|?*]+\\)*[^\s<>:"|?*]*`)
	assistantProjectNamePattern = regexp.MustCompile(`(?:项目名(?:叫|为|是)|项目名称(?:为|是)|命名为)\s*[“"']?([^，,。；;\n”"']{1,80})`)
	assistantAudiencePattern    = regexp.MustCompile(`面向\s*([^，,。；;\n]{1,80}?)(?:的?\s*\d{1,3}\s*秒|制作|打造|，|,|。|；|;|$)`)
	assistantDurationPattern    = regexp.MustCompile(`(\d{1,3})\s*秒`)
	assistantMustShowPattern    = regexp.MustCompile(`(?:重点展示|必须展示|需要展示)\s*([^。；;\n]{1,200})`)
	assistantObjectivePattern   = regexp.MustCompile(`(?:演示目标|目标|需求)(?:改为|修改为|是|为)\s*[“"']?([^。；;\n”"']{1,500})`)
)

type assistantModelResponse struct {
	Reply            string                          `json:"reply"`
	Patch            model.ProjectConfigurationPatch `json:"patch"`
	HasPatch         bool                            `json:"hasPatch"`
	MissingFields    []string                        `json:"missingFields"`
	SuggestedActions []assistantSuggestedAction      `json:"suggestedActions"`
	GenerationSource string                          `json:"-"`
	ModelProvider    string                          `json:"-"`
	ModelName        string                          `json:"-"`
	FallbackReason   string                          `json:"-"`
}

type assistantSuggestedAction struct {
	Kind              model.AssistantProposalKind `json:"kind"`
	Title             string                      `json:"title"`
	Description       string                      `json:"description"`
	TargetWorkstation model.AssistantWorkstation  `json:"targetWorkstation,omitempty"`
}

type assistantLLMSourceStatus struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Connected bool   `json:"connected"`
}

type assistantLLMConfiguration struct {
	ProjectName       string                     `json:"projectName,omitempty"`
	ProductURL        string                     `json:"productURL,omitempty"`
	Sources           []assistantLLMSourceStatus `json:"sources,omitempty"`
	Objective         string                     `json:"objective,omitempty"`
	TargetAudience    string                     `json:"targetAudience,omitempty"`
	TargetDurationSec int                        `json:"targetDurationSec,omitempty"`
	MustShow          []string                   `json:"mustShow,omitempty"`
	MustNotShow       []string                   `json:"mustNotShow,omitempty"`
	ForbiddenPages    []string                   `json:"forbiddenPages,omitempty"`
	ForbiddenData     []string                   `json:"forbiddenData,omitempty"`
	BrandTone         string                     `json:"brandTone,omitempty"`
	AllowedDomains    []string                   `json:"allowedDomains,omitempty"`
	Readiness         string                     `json:"readiness"`
	MissingFields     []string                   `json:"missingFields,omitempty"`
	Confirmed         bool                       `json:"confirmed"`
}

type assistantLLMWorkflow struct {
	ProjectAttached        bool                         `json:"projectAttached"`
	Stage                  string                       `json:"stage"`
	Status                 string                       `json:"status"`
	AvailableWorkstations  []model.AssistantWorkstation `json:"availableWorkstations"`
	RecommendedWorkstation model.AssistantWorkstation   `json:"recommendedWorkstation"`
	UploadApprovalRequired bool                         `json:"uploadApprovalRequired"`
	ResultReviewRequired   bool                         `json:"resultReviewRequired"`
}

func (s *Service) CreateAssistantSession(ctx context.Context, request model.AssistantContext) (*model.AssistantSession, error) {
	if request.Surface != model.AssistantSurfaceProjects && request.Surface != model.AssistantSurfaceRepositories {
		return nil, errors.New("unsupported assistant surface")
	}
	if strings.TrimSpace(request.ScopeKey) == "" {
		return nil, errors.New("assistant scope key is required")
	}
	id := assistantID(request.Surface, request.ScopeKey)
	if existing, err := s.assistantStore.Load(ctx, id); err == nil {
		return normalizeAssistantSession(existing), nil
	}
	now := time.Now().UTC()
	draft := newConfigurationDraft()
	activeWorkstation := model.AssistantWorkstationOverview
	workstationTitle := "项目配置"
	workstationStatus := "等待你的目标"
	if request.ProjectID != "" {
		if state, err := s.LoadProject(ctx, request.ProjectID); err == nil && state.ProjectContext != nil {
			draft = s.configurationFromProject(state.ProjectContext)
			if state.ExecutableScriptBundle != nil {
				draft.Confirmed = true
				draft.AnalysisProjectID = state.ProjectID
				activeWorkstation = model.AssistantWorkstationApproval
				workstationTitle = "执行包审批"
				workstationStatus = "本地方案已生成"
			} else if state.ProjectIntelligence != nil || state.SourceBinding != nil {
				draft.Confirmed = true
				draft.AnalysisProjectID = state.ProjectID
				activeWorkstation = model.AssistantWorkstationEvidence
				workstationTitle = "证据与产品理解"
				workstationStatus = "本地分析需要复核"
			}
			refreshConfigurationMetadata(&draft)
		}
	}
	session := &model.AssistantSession{
		ID: id, Context: request, Status: "waiting_for_user",
		ActiveWorkstation: activeWorkstation,
		WorkstationTitle:  workstationTitle, WorkstationStatus: workstationStatus,
		Configuration: draft,
		Messages: []model.AssistantMessage{{
			ID: "welcome", Role: "agent", Kind: "answer",
			Text:      "告诉我你想为哪个产品制作演示、面向谁，以及必须展示什么。我会先整理成可审阅的 configuration，不会自动上传或执行。",
			CreatedAt: now,
		}},
		ProcessedIdempotency: map[string]string{},
	}
	refreshAssistantNextAction(session)
	return session, s.assistantStore.Save(ctx, session)
}

func (s *Service) GetAssistantSession(ctx context.Context, sessionID string) (*model.AssistantSession, error) {
	session, err := s.assistantStore.Load(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return normalizeAssistantSession(session), nil
}

func (s *Service) SubmitAssistantTurn(ctx context.Context, sessionID string, request model.AssistantTurnRequest) (*model.AssistantSession, error) {
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	message := redactAssistantText(strings.TrimSpace(request.Message))
	if message == "" {
		return nil, errors.New("assistant message is required")
	}
	if len(message) > 6000 {
		return nil, errors.New("assistant message is too long")
	}
	session, err := s.GetAssistantSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if request.IdempotencyKey != "" {
		if _, ok := session.ProcessedIdempotency[request.IdempotencyKey]; ok {
			return session, nil
		}
	}
	now := time.Now().UTC()
	session.Status = "thinking"
	session.Messages = append(session.Messages, model.AssistantMessage{
		ID: fmt.Sprintf("user_%d", now.UnixNano()), Role: "user", Kind: "answer", Text: message, CreatedAt: now,
	})
	workflow := s.assistantWorkflowProjection(ctx, *session)
	response := s.generateAssistantResponse(ctx, session.Configuration, workflow, message)
	if len(request.SelectedSources) > 0 || len(request.CredentialRefs) > 0 {
		patch, patchErr := s.patchForSafeSelections(session.Configuration, request.SelectedSources, request.CredentialRefs)
		if patchErr != nil {
			return nil, patchErr
		}
		response = assistantModelResponse{Reply: "安全选择已完成。请确认将 opaque refs 写入 configuration；文件内容和凭据不会进入聊天。", Patch: patch, HasPatch: true}
	}
	proposals := s.proposalsFromModelResponse(session, workflow, response, now)
	text := strings.TrimSpace(redactAssistantText(response.Reply))
	if text == "" {
		text = "我已整理这轮信息。请检查字段变更；确认后才会写入项目 configuration。"
	}
	kind := "answer"
	if len(proposals) > 0 {
		kind = "proposal"
		session.Status = "awaiting_confirmation"
		session.WorkstationStatus = "等待确认字段变更"
	} else {
		session.Status = "waiting_for_user"
		session.WorkstationStatus = "等待补充信息"
	}
	session.Messages = append(session.Messages, model.AssistantMessage{
		ID: fmt.Sprintf("agent_%d", now.UnixNano()+1), Role: "agent", Kind: kind, Text: text,
		GenerationSource: response.GenerationSource, ModelProvider: response.ModelProvider,
		ModelName: response.ModelName, FallbackReason: response.FallbackReason,
		CreatedAt: now, TargetWorkstation: model.AssistantWorkstationOverview, Proposals: proposals,
	})
	refreshAssistantNextAction(session)
	event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()), SessionID: session.ID, Type: "message", Text: text, CreatedAt: now}
	session.Events = append(session.Events, event)
	session.LastEventID = event.ID
	if request.IdempotencyKey != "" {
		session.ProcessedIdempotency[request.IdempotencyKey] = event.ID
	}
	refreshAssistantNextAction(session)
	if err := s.assistantStore.Save(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

func (s *Service) ProposeAssistantConfigurationPatch(ctx context.Context, sessionID string, request model.AssistantConfigurationProposalRequest) (*model.AssistantSession, error) {
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return nil, errors.New("idempotency key is required")
	}
	session, err := s.GetAssistantSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if _, ok := session.ProcessedIdempotency[request.IdempotencyKey]; ok {
		return session, nil
	}
	if request.BaseVersion != session.Configuration.Version {
		return nil, fmt.Errorf("configuration version conflict: current=%d requested=%d", session.Configuration.Version, request.BaseVersion)
	}
	patch := sanitizeConfigurationPatch(request.Patch)
	// Sources and credentials always use the trusted picker/credential actions.
	if patch.Sources != nil || patch.CredentialRefs != nil {
		return nil, errors.New("manual configuration cannot set source or credential refs")
	}
	if configurationPatchEmpty(patch) {
		return nil, errors.New("configuration patch is empty")
	}
	if _, err := applyConfigurationPatch(session.Configuration, patch); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	proposal := newAssistantProposal(model.AssistantProposalConfigurationPatch, "确认手动 configuration 变更", "应用表单中的字段；确认前不会写入、分析或上传。", session.Configuration.Version, now, &patch, model.AssistantWorkstationOverview)
	session.Messages = append(session.Messages, model.AssistantMessage{
		ID: fmt.Sprintf("manual_%d", now.UnixNano()), Role: "agent", Kind: "proposal",
		Text:             "已将手动填写内容整理为同一套受控 configuration 提案，请核对后确认。",
		GenerationSource: "manual", CreatedAt: now, TargetWorkstation: model.AssistantWorkstationOverview,
		Proposals: []model.AssistantProposal{proposal},
	})
	session.Status = "awaiting_confirmation"
	session.WorkstationStatus = "等待确认手动字段变更"
	event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()), SessionID: session.ID, Type: "configuration_proposal", Text: "manual configuration proposal created", CreatedAt: now}
	session.Events = append(session.Events, event)
	session.LastEventID = event.ID
	session.ProcessedIdempotency[request.IdempotencyKey] = event.ID
	refreshAssistantNextAction(session)
	if err := s.assistantStore.Save(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

func (s *Service) patchForSafeSelections(current model.ProjectConfigurationDraft, sources []model.ConfigurationSourceRef, credentialRefs []string) (model.ProjectConfigurationPatch, error) {
	nextSources := append([]model.ConfigurationSourceRef{}, current.Sources...)
	seenSources := map[string]bool{}
	for _, source := range nextSources {
		seenSources[source.Ref+"|"+source.URL] = true
	}
	for _, source := range sources {
		if source.Kind == "github_repository" {
			parsed, err := url.Parse(source.URL)
			if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "github.com" {
				return model.ProjectConfigurationPatch{}, errors.New("GitHub source must use an https://github.com URL")
			}
		} else {
			resolved, ok := s.ResolveLocalSourceRef(source.Ref)
			if !ok || resolved.Kind != source.Kind {
				return model.ProjectConfigurationPatch{}, errors.New("local source ref is unavailable")
			}
			source.Label = resolved.Label
			source.URL = ""
		}
		key := source.Ref + "|" + source.URL
		if !seenSources[key] {
			nextSources = append(nextSources, source)
			seenSources[key] = true
		}
	}
	nextCredentials := append([]string{}, current.CredentialRefs...)
	seenCredentials := map[string]bool{}
	for _, ref := range nextCredentials {
		seenCredentials[ref] = true
	}
	for _, ref := range credentialRefs {
		if !strings.HasPrefix(ref, "credential://demo/") {
			return model.ProjectConfigurationPatch{}, errors.New("unsupported credential ref")
		}
		if _, err := credentialstore.ReadDemoCredential(strings.TrimPrefix(ref, "credential://demo/")); err != nil {
			return model.ProjectConfigurationPatch{}, errors.New("demo credential ref is unavailable")
		}
		if !seenCredentials[ref] {
			nextCredentials = append(nextCredentials, ref)
			seenCredentials[ref] = true
		}
	}
	patch := model.ProjectConfigurationPatch{}
	if len(sources) > 0 {
		patch.Sources = &nextSources
	}
	if len(credentialRefs) > 0 {
		patch.CredentialRefs = &nextCredentials
	}
	return patch, nil
}

func (s *Service) generateAssistantResponse(ctx context.Context, draft model.ProjectConfigurationDraft, workflow assistantLLMWorkflow, message string) assistantModelResponse {
	response := assistantFallbackResponse(draft, message)
	workflowActions := deterministicWorkflowActions(message, workflow)
	if len(workflowActions) > 0 {
		response.Patch = model.ProjectConfigurationPatch{}
		response.HasPatch = false
		response.Reply = "已找到当前项目中可访问的工作台。确认卡片只会切换视图，不会执行审批、上传、下载或审核。"
	}
	response.SuggestedActions = mergeAssistantActions(workflowActions, response.SuggestedActions)
	response.GenerationSource = "deterministic_fallback"
	response.FallbackReason = "model_unavailable"
	if s.llm == nil {
		return response
	}
	configurationJSON, _ := json.Marshal(assistantLLMConfigurationProjection(draft))
	workflowJSON, _ := json.Marshal(workflow)
	var generated assistantModelResponse
	trace, err := s.llm.GenerateJSON(ctx, config.ModelTaskPlanning, llm.JSONRequest{
		System:     "你是 DemoOps 项目配置与流程引导 Agent。只提取用户明确提供的信息，不猜测凭据、路径或业务事实。你只能返回 configuration patch 和受限建议动作，不能执行命令、访问 URL、审批上传、下载成品或提交审核。open_workstation 只能指向 availableWorkstations 中的值。不要在 reply 中复述密码、token、完整本地路径、源码、执行包内容或错误日志。sources[].connected=true 表示该来源已通过安全选择器连接；其真实 ref 和路径被有意隐藏，不得因此要求用户再次选择来源或补充 ref。",
		User:       fmt.Sprintf("当前 configuration：%s\n当前 workflow：%s\n用户消息：%s", configurationJSON, workflowJSON, message),
		SchemaName: "demoops_assistant_configuration_proposal_v1", MaxTokens: 1800, Temperature: 0.1,
		ResponseHint: "reply:string, hasPatch:boolean, patch:ProjectConfigurationPatch, missingFields:string[], suggestedActions:{kind,title,description,targetWorkstation}[]。kind 只能是 configuration_patch/select_local_project/connect_github/attach_requirement_document/attach_brand_asset/store_demo_credential/confirm_configuration/start_local_analysis/open_workstation/continue_with_webpage_evidence。Sources 不能包含本地绝对路径或 secret。",
	}, &generated)
	if err != nil {
		if trace != nil {
			response.ModelProvider = string(trace.Provider)
			response.ModelName = trace.Model
			response.FallbackReason = firstNonEmptyString(trace.FallbackReason, trace.ErrorClass, "model_call_failed")
		}
		return response
	}
	generated.GenerationSource = "llm"
	if trace != nil {
		generated.ModelProvider = string(trace.Provider)
		generated.ModelName = trace.Model
	}
	explicit := assistantFallbackResponse(draft, message)
	if (len(explicit.SuggestedActions) > 0 && !explicit.HasPatch) || len(workflowActions) > 0 {
		// A pure picker/credential request is an action, not a project objective.
		// Ignore any model-invented configuration fields for that turn.
		generated.Patch = model.ProjectConfigurationPatch{}
		generated.HasPatch = false
	}
	if len(workflowActions) > 0 {
		explicit.Patch = model.ProjectConfigurationPatch{}
		explicit.HasPatch = false
	}
	generated.Patch = mergeExplicitAssistantPatch(sanitizeConfigurationPatch(generated.Patch), explicit.Patch)
	generated.SuggestedActions = mergeAssistantActions(generated.SuggestedActions, explicit.SuggestedActions)
	generated.SuggestedActions = mergeAssistantActions(workflowActions, generated.SuggestedActions)
	// Source and credential refs are created only by trusted picker/credential paths.
	generated.Patch.Sources = nil
	generated.Patch.CredentialRefs = nil
	generated.HasPatch = generated.HasPatch || !configurationPatchEmpty(generated.Patch)
	generated.Reply = redactAssistantText(generated.Reply)
	return generated
}

func assistantLLMConfigurationProjection(draft model.ProjectConfigurationDraft) assistantLLMConfiguration {
	projected := assistantLLMConfiguration{
		ProjectName: draft.ProjectName, ProductURL: draft.ProductURL, Objective: draft.Objective,
		TargetAudience: draft.TargetAudience, TargetDurationSec: draft.TargetDurationSec,
		MustShow: append([]string{}, draft.MustShow...), MustNotShow: append([]string{}, draft.MustNotShow...),
		ForbiddenPages: append([]string{}, draft.ForbiddenPages...), ForbiddenData: append([]string{}, draft.ForbiddenData...),
		BrandTone: draft.BrandTone, AllowedDomains: append([]string{}, draft.AllowedDomains...),
		Readiness: draft.Readiness, MissingFields: append([]string{}, draft.MissingFields...), Confirmed: draft.Confirmed,
		Sources: make([]assistantLLMSourceStatus, 0, len(draft.Sources)),
	}
	for index, source := range draft.Sources {
		projected.Sources = append(projected.Sources, assistantLLMSourceStatus{
			Kind: source.Kind, Label: fmt.Sprintf("source_%d", index+1), Connected: true,
		})
	}
	return projected
}

func (s *Service) assistantWorkflowProjection(ctx context.Context, session model.AssistantSession) assistantLLMWorkflow {
	workflow := assistantLLMWorkflow{
		Stage: "configuration", Status: "waiting_for_configuration",
		AvailableWorkstations:  []model.AssistantWorkstation{model.AssistantWorkstationOverview},
		RecommendedWorkstation: model.AssistantWorkstationOverview,
		UploadApprovalRequired: true, ResultReviewRequired: true,
	}
	projectID := firstNonEmptyString(session.Context.ProjectID, session.Configuration.AnalysisProjectID)
	if projectID == "" {
		if session.Configuration.Readiness == "ready" {
			workflow.Status = "waiting_for_configuration_confirmation"
		}
		return workflow
	}
	workflow.ProjectAttached = true
	workflow.Stage = "local_analysis"
	workflow.Status = "analysis_started"
	workflow.AvailableWorkstations = append(workflow.AvailableWorkstations, model.AssistantWorkstationEvidence)
	workflow.RecommendedWorkstation = model.AssistantWorkstationEvidence
	state, err := s.LoadProject(ctx, projectID)
	if err != nil || state == nil {
		workflow.Status = "project_state_unavailable"
		return workflow
	}
	if state.ProjectIntelligence != nil || state.UnderstandingReport != nil || state.WorkflowGraph != nil {
		workflow.Stage = "plan_review"
		workflow.Status = "local_analysis_ready"
		workflow.AvailableWorkstations = appendAssistantWorkstation(workflow.AvailableWorkstations, model.AssistantWorkstationPlan)
		workflow.RecommendedWorkstation = model.AssistantWorkstationPlan
	}
	if state.ExecutableScriptBundle != nil {
		workflow.Stage = "package_approval"
		workflow.Status = "waiting_for_upload_approval"
		workflow.AvailableWorkstations = appendAssistantWorkstation(workflow.AvailableWorkstations, model.AssistantWorkstationApproval)
		workflow.RecommendedWorkstation = model.AssistantWorkstationApproval
	}
	cloud := state.DesktopCloudRun
	if cloud == nil {
		if state.SourceBinding != nil && state.SourceBinding.EffectiveMode == model.ProductSourceModeBlocked {
			workflow.Stage = "source_binding_review"
			workflow.Status = "source_binding_blocked"
			workflow.RecommendedWorkstation = model.AssistantWorkstationEvidence
		} else if state.ExecutableScriptBundle != nil && (state.Status == orchestrator.FlowStatusFailed || strings.TrimSpace(state.ErrorMessage) != "") {
			workflow.Stage = "repair"
			workflow.Status = "local_analysis_blocked"
			workflow.AvailableWorkstations = appendAssistantWorkstation(workflow.AvailableWorkstations, model.AssistantWorkstationRepair)
			workflow.RecommendedWorkstation = model.AssistantWorkstationRepair
		}
		return workflow
	}
	if cloud.ExchangePackageID != "" || cloud.CloudJobID != "" || cloud.Status == "queued" || cloud.Status == "running" {
		workflow.Stage = "server_execution"
		workflow.Status = firstNonEmptyString(cloud.Status, "submitted")
		workflow.AvailableWorkstations = appendAssistantWorkstation(workflow.AvailableWorkstations, model.AssistantWorkstationExecution)
		workflow.RecommendedWorkstation = model.AssistantWorkstationExecution
	}
	if cloud.Status == "failed" || cloud.Error != nil || cloud.FailureSummary != nil {
		workflow.Stage = "repair"
		workflow.Status = "execution_failed"
		workflow.AvailableWorkstations = appendAssistantWorkstation(workflow.AvailableWorkstations, model.AssistantWorkstationRepair)
		workflow.RecommendedWorkstation = model.AssistantWorkstationRepair
	}
	if cloud.ResultPackage != nil || cloud.ResultPackageID != "" {
		workflow.Stage = "result_review"
		workflow.Status = "result_available"
		workflow.AvailableWorkstations = appendAssistantWorkstation(workflow.AvailableWorkstations, model.AssistantWorkstationAssets)
		workflow.RecommendedWorkstation = model.AssistantWorkstationAssets
		if cloud.ResultDownloaded {
			workflow.AvailableWorkstations = appendAssistantWorkstation(workflow.AvailableWorkstations, model.AssistantWorkstationEditor)
		}
		if cloud.ResultReview != nil && cloud.ResultReview.Decision == "approved" {
			workflow.Status = "result_approved"
			workflow.ResultReviewRequired = false
		}
	}
	return workflow
}

func appendAssistantWorkstation(values []model.AssistantWorkstation, value model.AssistantWorkstation) []model.AssistantWorkstation {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func assistantWorkstationAvailable(workflow assistantLLMWorkflow, target model.AssistantWorkstation) bool {
	for _, available := range workflow.AvailableWorkstations {
		if available == target {
			return true
		}
	}
	return false
}

func assistantFallbackResponse(draft model.ProjectConfigurationDraft, message string) assistantModelResponse {
	patch := model.ProjectConfigurationPatch{}
	hasPatch := false
	if candidate := firstHTTPURL(message); candidate != "" {
		parsed, _ := url.Parse(candidate)
		if !strings.EqualFold(parsed.Hostname(), "github.com") || len(deterministicAssistantActions(message)) == 0 {
			patch.ProductURL = &candidate
			hasPatch = true
		}
	}
	objective := ""
	if match := assistantObjectivePattern.FindStringSubmatch(message); len(match) > 1 {
		objective = strings.TrimSpace(match[1])
	} else if strings.TrimSpace(draft.Objective) == "" && len(deterministicAssistantActions(message)) == 0 {
		objective = strings.TrimSpace(redactAssistantText(message))
	}
	if objective != "" && len([]rune(objective)) <= 500 {
		patch.Objective = &objective
		hasPatch = true
	}
	if match := assistantProjectNamePattern.FindStringSubmatch(message); len(match) > 1 {
		value := strings.TrimSpace(match[1])
		patch.ProjectName = &value
		hasPatch = true
	}
	if match := assistantAudiencePattern.FindStringSubmatch(message); len(match) > 1 {
		value := strings.TrimSpace(match[1])
		patch.TargetAudience = &value
		hasPatch = true
	}
	if match := assistantDurationPattern.FindStringSubmatch(message); len(match) > 1 {
		var value int
		if _, err := fmt.Sscanf(match[1], "%d", &value); err == nil && value >= 5 && value <= 600 {
			patch.TargetDurationSec = &value
			hasPatch = true
		}
	}
	if match := assistantMustShowPattern.FindStringSubmatch(message); len(match) > 1 {
		value := []string{strings.TrimSpace(match[1])}
		patch.MustShow = &value
		hasPatch = true
	}
	return assistantModelResponse{
		Reply: "我会把这轮信息整理为 configuration 变更。确认前不会保存，也不会启动分析。",
		Patch: patch, HasPatch: hasPatch, SuggestedActions: deterministicAssistantActions(message),
	}
}

func deterministicAssistantActions(message string) []assistantSuggestedAction {
	normalized := strings.ToLower(strings.TrimSpace(message))
	actions := []assistantSuggestedAction{}
	add := func(kind model.AssistantProposalKind, title, description string) {
		for _, action := range actions {
			if action.Kind == kind {
				return
			}
		}
		actions = append(actions, assistantSuggestedAction{Kind: kind, Title: title, Description: description, TargetWorkstation: model.AssistantWorkstationOverview})
	}
	if strings.Contains(normalized, "github") || strings.Contains(normalized, "git hub") {
		add(model.AssistantProposalConnectGitHub, "连接 GitHub 仓库", "通过安全卡片添加 GitHub HTTPS URL；私有仓库授权不会进入聊天。")
	}
	if (strings.Contains(normalized, "本地") || strings.Contains(normalized, "local")) && (strings.Contains(normalized, "项目") || strings.Contains(normalized, "源码") || strings.Contains(normalized, "仓库") || strings.Contains(normalized, "目录")) {
		add(model.AssistantProposalSelectLocalProject, "选择本地项目目录", "使用桌面原生目录选择器，只把 opaque source ref 写入 configuration。")
	}
	if strings.Contains(normalized, "需求文档") || strings.Contains(normalized, "requirement document") {
		add(model.AssistantProposalAttachRequirementDocument, "添加需求文档", "使用桌面原生文件选择器添加需求文档的安全引用。")
	}
	if strings.Contains(normalized, "品牌素材") || strings.Contains(normalized, "brand asset") || strings.Contains(normalized, "logo") {
		add(model.AssistantProposalAttachBrandAsset, "添加品牌素材", "使用桌面原生文件选择器添加品牌素材的安全引用。")
	}
	if strings.Contains(normalized, "测试账号") || strings.Contains(normalized, "演示账号") || strings.Contains(normalized, "demo credential") {
		add(model.AssistantProposalStoreDemoCredential, "安全保存演示账号", "账号内容只进入本机凭据库，configuration 仅保存 opaque secret ref。")
	}
	return actions
}

func deterministicWorkflowActions(message string, workflow assistantLLMWorkflow) []assistantSuggestedAction {
	normalized := strings.ToLower(strings.TrimSpace(message))
	target := model.AssistantWorkstation("")
	switch {
	case containsAnyAssistantText(normalized, "证据", "来源一致", "evidence"):
		target = model.AssistantWorkstationEvidence
	case containsAnyAssistantText(normalized, "方案", "计划", "剧本", "大纲", "plan", "outline"):
		target = model.AssistantWorkstationPlan
	case containsAnyAssistantText(normalized, "审批", "执行包", "上传", "approval", "package"):
		target = model.AssistantWorkstationApproval
	case containsAnyAssistantText(normalized, "进度", "执行状态", "录制状态", "execution", "progress"):
		target = model.AssistantWorkstationExecution
	case containsAnyAssistantText(normalized, "修复", "失败诊断", "repair"):
		target = model.AssistantWorkstationRepair
	case containsAnyAssistantText(normalized, "成品", "审核视频", "结果", "assets", "result"):
		target = model.AssistantWorkstationAssets
	case containsAnyAssistantText(normalized, "编辑器", "剪辑", "editor"):
		target = model.AssistantWorkstationEditor
	}
	if target == "" || !assistantWorkstationAvailable(workflow, target) {
		return nil
	}
	return []assistantSuggestedAction{{
		Kind: model.AssistantProposalOpenWorkstation, Title: "打开" + assistantWorkstationTitle(target),
		Description: "切换到已解锁的工作台查看当前结果；不会执行审批、上传、下载或审核。", TargetWorkstation: target,
	}}
}

func containsAnyAssistantText(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func mergeAssistantActions(primary, deterministic []assistantSuggestedAction) []assistantSuggestedAction {
	result := append([]assistantSuggestedAction{}, primary...)
	seen := map[model.AssistantProposalKind]bool{}
	for _, action := range result {
		seen[action.Kind] = true
	}
	for _, action := range deterministic {
		if !seen[action.Kind] {
			result = append(result, action)
			seen[action.Kind] = true
		}
	}
	return result
}

func mergeExplicitAssistantPatch(primary, explicit model.ProjectConfigurationPatch) model.ProjectConfigurationPatch {
	if primary.ProjectName == nil {
		primary.ProjectName = explicit.ProjectName
	}
	if primary.ProductURL == nil {
		primary.ProductURL = explicit.ProductURL
	}
	if primary.Objective == nil {
		primary.Objective = explicit.Objective
	}
	if primary.TargetAudience == nil {
		primary.TargetAudience = explicit.TargetAudience
	}
	if primary.TargetDurationSec == nil {
		primary.TargetDurationSec = explicit.TargetDurationSec
	}
	if primary.MustShow == nil {
		primary.MustShow = explicit.MustShow
	}
	return primary
}

func (s *Service) proposalsFromModelResponse(session *model.AssistantSession, workflow assistantLLMWorkflow, response assistantModelResponse, now time.Time) []model.AssistantProposal {
	proposals := make([]model.AssistantProposal, 0, len(response.SuggestedActions)+1)
	hasPatch := response.HasPatch && !configurationPatchEmpty(response.Patch)
	if hasPatch {
		proposals = append(proposals, newAssistantProposal(model.AssistantProposalConfigurationPatch, "确认 configuration 变更", "应用本轮提取的字段；不会启动分析或上传。", session.Configuration.Version, now, &response.Patch, ""))
	}
	for _, action := range response.SuggestedActions {
		// A configuration patch changes the version. Present client actions on the
		// follow-up turn so sibling proposals cannot become stale by construction.
		if hasPatch {
			continue
		}
		if !model.IsAssistantProposalKind(action.Kind) || action.Kind == model.AssistantProposalConfigurationPatch {
			continue
		}
		if action.Kind == model.AssistantProposalOpenWorkstation && !model.IsAssistantWorkstation(action.TargetWorkstation) {
			continue
		}
		if !model.IsAssistantWorkstation(action.TargetWorkstation) {
			action.TargetWorkstation = model.AssistantWorkstationOverview
		}
		if !assistantActionAllowed(*session, workflow, action) {
			continue
		}
		if assistantProposalAlreadyAvailable(session, action.Kind, action.TargetWorkstation) {
			continue
		}
		proposals = append(proposals, newAssistantProposal(action.Kind, redactAssistantText(action.Title), redactAssistantText(action.Description), session.Configuration.Version, now, nil, action.TargetWorkstation))
	}
	if len(proposals) == 0 && session.Configuration.Readiness == "ready" && !session.Configuration.Confirmed && !assistantProposalAlreadyAvailable(session, model.AssistantProposalConfirmConfiguration, "") {
		proposals = append(proposals, newAssistantProposal(model.AssistantProposalConfirmConfiguration, "确认 configuration 并启动本地理解", "锁定当前摘要并启动本地代码与需求分析；执行包上传仍需单独审批。", session.Configuration.Version, now, nil, model.AssistantWorkstationEvidence))
	}
	return proposals
}

func assistantProposalAlreadyAvailable(session *model.AssistantSession, kind model.AssistantProposalKind, workstation model.AssistantWorkstation) bool {
	if session == nil {
		return false
	}
	for messageIndex := range session.Messages {
		for proposalIndex := range session.Messages[messageIndex].Proposals {
			proposal := session.Messages[messageIndex].Proposals[proposalIndex]
			if proposal.Status == "available" && proposal.Kind == kind && (workstation == "" || proposal.TargetWorkstation == workstation) {
				return true
			}
		}
	}
	return false
}

func assistantActionAllowed(session model.AssistantSession, workflow assistantLLMWorkflow, action assistantSuggestedAction) bool {
	switch action.Kind {
	case model.AssistantProposalConfirmConfiguration, model.AssistantProposalStartLocalAnalysis:
		return session.Configuration.Readiness == "ready" && !session.Configuration.Confirmed
	case model.AssistantProposalContinueWithWebpageEvidence:
		return workflow.Status == "source_binding_blocked"
	case model.AssistantProposalOpenWorkstation:
		return assistantWorkstationAvailable(workflow, action.TargetWorkstation)
	default:
		return true
	}
}

func newAssistantProposal(kind model.AssistantProposalKind, title, description string, baseVersion int64, now time.Time, patch *model.ProjectConfigurationPatch, workstation model.AssistantWorkstation) model.AssistantProposal {
	seed := fmt.Sprintf("%s:%d:%d:%s", kind, baseVersion, now.UnixNano(), title)
	digest := sha256.Sum256([]byte(seed))
	id := "proposal_" + hex.EncodeToString(digest[:8])
	return model.AssistantProposal{
		ID: id, Kind: kind, Title: title, Description: description, TargetWorkstation: workstation,
		Patch: patch, BaseVersion: baseVersion, IdempotencyKey: id, RequiresConfirmation: true, Status: "available",
	}
}

func (s *Service) ListAssistantEvents(ctx context.Context, sessionID, after string) ([]model.AssistantEvent, error) {
	session, err := s.GetAssistantSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]model.AssistantEvent, 0, len(session.Events))
	for _, event := range session.Events {
		if after == "" || event.ID > after {
			result = append(result, event)
		}
	}
	return result, nil
}

func (s *Service) ConfirmAssistantProposal(ctx context.Context, sessionID, proposalID string, request model.AssistantProposalDecisionRequest) (*model.AssistantSession, error) {
	return s.updateAssistantProposal(ctx, sessionID, proposalID, "confirmed", request)
}

func (s *Service) DismissAssistantProposal(ctx context.Context, sessionID, proposalID string, request model.AssistantProposalDecisionRequest) (*model.AssistantSession, error) {
	return s.updateAssistantProposal(ctx, sessionID, proposalID, "dismissed", request)
}

func (s *Service) CancelAssistantSession(ctx context.Context, sessionID string) (*model.AssistantSession, error) {
	session, err := s.GetAssistantSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	session.Status = "idle"
	refreshAssistantNextAction(session)
	return session, s.assistantStore.Save(ctx, session)
}

func (s *Service) CompleteAssistantClientAction(ctx context.Context, sessionID, proposalID string, request model.AssistantClientActionResultRequest) (*model.AssistantSession, error) {
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	session, err := s.GetAssistantSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if request.IdempotencyKey != "" {
		if _, ok := session.ProcessedIdempotency[request.IdempotencyKey]; ok {
			return session, nil
		}
	}
	proposal := findAssistantProposal(session, proposalID)
	if proposal == nil || proposal.Status != "available" {
		return nil, errors.New("assistant client action is unavailable")
	}
	if request.BaseVersion != 0 && request.BaseVersion != session.Configuration.Version || proposal.BaseVersion != session.Configuration.Version {
		return nil, fmt.Errorf("configuration version conflict: current=%d proposal=%d", session.Configuration.Version, proposal.BaseVersion)
	}
	if err := validateAssistantClientActionResult(proposal.Kind, request); err != nil {
		return nil, err
	}
	patch, err := s.patchForSafeSelections(session.Configuration, request.SelectedSources, request.CredentialRefs)
	if err != nil {
		return nil, err
	}
	next, err := applyConfigurationPatch(session.Configuration, patch)
	if err != nil {
		return nil, err
	}
	session.Configuration = next
	invalidateAssistantAnalysisContext(session)
	proposal.Status = "confirmed"
	proposal.ExecutionResult = map[string]any{"configurationVersion": next.Version, "configurationHash": next.Hash, "clientActionCompleted": true}
	dismissStaleAssistantProposals(session, proposal.ID, next.Version)
	now := time.Now().UTC()
	if followUp := configurationFollowUpMessage(next, now); followUp != nil {
		session.Messages = append(session.Messages, *followUp)
	}
	refreshAssistantPendingStatus(session)
	event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()), SessionID: session.ID, Type: "client_action", Text: string(proposal.Kind) + ": completed", CreatedAt: now}
	session.Events = append(session.Events, event)
	session.LastEventID = event.ID
	key := strings.TrimSpace(request.IdempotencyKey)
	if key == "" {
		key = proposal.IdempotencyKey + ":completed"
	}
	session.ProcessedIdempotency[key] = event.ID
	refreshAssistantNextAction(session)
	if err := s.assistantStore.Save(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

func validateAssistantClientActionResult(kind model.AssistantProposalKind, request model.AssistantClientActionResultRequest) error {
	sourceKinds := map[string]bool{}
	for _, source := range request.SelectedSources {
		sourceKinds[source.Kind] = true
	}
	switch kind {
	case model.AssistantProposalSelectProjectSource:
		if len(request.SelectedSources) != 1 || (!sourceKinds["local_repository"] && !sourceKinds["github_repository"]) {
			return errors.New("select_project_source requires one local or GitHub repository")
		}
	case model.AssistantProposalSelectLocalProject:
		if len(request.SelectedSources) != 1 || !sourceKinds["local_repository"] {
			return errors.New("select_local_project requires one local repository")
		}
	case model.AssistantProposalConnectGitHub:
		if len(request.SelectedSources) != 1 || !sourceKinds["github_repository"] {
			return errors.New("connect_github requires one GitHub repository")
		}
	case model.AssistantProposalAttachRequirementDocument:
		if len(request.SelectedSources) == 0 || !sourceKinds["requirement_document"] {
			return errors.New("attach_requirement_document requires document refs")
		}
	case model.AssistantProposalAttachBrandAsset:
		if len(request.SelectedSources) == 0 || !sourceKinds["brand_asset"] {
			return errors.New("attach_brand_asset requires asset refs")
		}
	case model.AssistantProposalStoreDemoCredential:
		if len(request.CredentialRefs) != 1 {
			return errors.New("store_demo_credential requires one credential ref")
		}
	default:
		return errors.New("proposal does not accept a client action result")
	}
	return nil
}

func (s *Service) updateAssistantProposal(ctx context.Context, sessionID, proposalID, status string, request model.AssistantProposalDecisionRequest) (*model.AssistantSession, error) {
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	session, err := s.GetAssistantSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	key := request.IdempotencyKey
	if key != "" {
		if _, ok := session.ProcessedIdempotency[key]; ok {
			return session, nil
		}
	}
	proposal := findAssistantProposal(session, proposalID)
	if proposal == nil {
		return nil, errors.New("assistant proposal not found")
	}
	if proposal.Status != "available" {
		return session, nil
	}
	var followUp *model.AssistantMessage
	if status == "confirmed" {
		if request.BaseVersion != 0 && request.BaseVersion != session.Configuration.Version {
			return nil, fmt.Errorf("configuration version conflict: current=%d requested=%d", session.Configuration.Version, request.BaseVersion)
		}
		if proposal.BaseVersion != session.Configuration.Version {
			return nil, fmt.Errorf("configuration version conflict: current=%d proposal=%d", session.Configuration.Version, proposal.BaseVersion)
		}
		followUp, err = s.executeAssistantProposal(ctx, session, proposal)
		if err != nil {
			return nil, err
		}
	}
	proposal.Status = status
	proposalKind := proposal.Kind
	proposalKey := proposal.IdempotencyKey
	if followUp != nil {
		session.Messages = append(session.Messages, *followUp)
	}
	refreshAssistantPendingStatus(session)
	refreshAssistantNextAction(session)
	now := time.Now().UTC()
	event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()), SessionID: session.ID, Type: "proposal", Text: string(proposalKind) + ": " + status, CreatedAt: now}
	session.Events = append(session.Events, event)
	session.LastEventID = event.ID
	if key == "" {
		key = proposalKey + ":" + status
	}
	session.ProcessedIdempotency[key] = event.ID
	if err := s.assistantStore.Save(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

func (s *Service) executeAssistantProposal(ctx context.Context, session *model.AssistantSession, proposal *model.AssistantProposal) (*model.AssistantMessage, error) {
	switch proposal.Kind {
	case model.AssistantProposalConfigurationPatch:
		if proposal.Patch == nil {
			return nil, errors.New("configuration patch is missing")
		}
		next, err := applyConfigurationPatch(session.Configuration, *proposal.Patch)
		if err != nil {
			return nil, err
		}
		session.Configuration = next
		invalidateAssistantAnalysisContext(session)
		dismissStaleAssistantProposals(session, proposal.ID, next.Version)
		proposal.ExecutionResult = map[string]any{"configurationVersion": next.Version, "configurationHash": next.Hash}
		return configurationFollowUpMessage(next, time.Now().UTC()), nil
	case model.AssistantProposalConfirmConfiguration, model.AssistantProposalStartLocalAnalysis:
		if session.Configuration.Readiness != "ready" {
			return nil, fmt.Errorf("configuration is incomplete: %s", strings.Join(session.Configuration.MissingFields, ", "))
		}
		input, err := s.userInputFromConfiguration(session.Configuration)
		if err != nil {
			return nil, err
		}
		state, err := s.CreateProject(ctx, input)
		if err != nil {
			if state == nil || state.ProjectID == "" {
				return nil, err
			}
			// Analysis gates (for example source mismatch or missing evidence)
			// persist a repairable project. Return that project to the workstation
			// without pretending the analysis or upload succeeded.
			now := time.Now().UTC()
			if state.ProjectContext != nil {
				state.ProjectContext.Name = session.Configuration.ProjectName
				if saveErr := s.states.Save(ctx, state); saveErr != nil {
					return nil, saveErr
				}
			}
			session.Configuration.Confirmed = true
			session.Configuration.ConfirmedAt = &now
			session.Configuration.AnalysisProjectID = state.ProjectID
			session.Context.ProjectID = state.ProjectID
			session.ActiveWorkstation = model.AssistantWorkstationEvidence
			session.WorkstationTitle = "本地分析需要处理"
			dismissAssistantProposalsOfKinds(session, proposal.ID, model.AssistantProposalConfirmConfiguration, model.AssistantProposalStartLocalAnalysis)
			info := bridgeErrorInfo(err)
			proposal.ExecutionResult = map[string]any{"projectID": state.ProjectID, "analysisStarted": true, "analysisBlocked": true, "errorCode": info.Code, "uploadApproved": false}
			return &model.AssistantMessage{
				ID: fmt.Sprintf("agent_analysis_blocked_%d", now.UnixNano()), Role: "agent", Kind: "error",
				Text: info.Message + "。项目已保存在本机，请在右侧证据工作台处理后重新分析。", CreatedAt: now,
				TargetWorkstation: model.AssistantWorkstationEvidence,
			}, nil
		}
		if state.ProjectContext != nil {
			state.ProjectContext.Name = session.Configuration.ProjectName
			if err := s.states.Save(ctx, state); err != nil {
				return nil, err
			}
		}
		now := time.Now().UTC()
		session.Configuration.Confirmed = true
		session.Configuration.ConfirmedAt = &now
		session.Configuration.AnalysisProjectID = state.ProjectID
		session.Context.ProjectID = state.ProjectID
		session.ActiveWorkstation = model.AssistantWorkstationApproval
		session.WorkstationTitle = "执行包审批"
		dismissAssistantProposalsOfKinds(session, proposal.ID, model.AssistantProposalConfirmConfiguration, model.AssistantProposalStartLocalAnalysis)
		proposal.ExecutionResult = map[string]any{"projectID": state.ProjectID, "analysisStarted": true, "uploadApproved": false}
		return &model.AssistantMessage{
			ID: fmt.Sprintf("agent_analysis_ready_%d", now.UnixNano()), Role: "agent", Kind: "status",
			Text:      "本地代码、网页证据和用户需求已完成分析，三合一执行包草稿已生成。请在右侧检查方案与风险；上传仍需独立人工审批。",
			CreatedAt: now, TargetWorkstation: model.AssistantWorkstationApproval,
		}, nil
	case model.AssistantProposalOpenWorkstation:
		workflow := s.assistantWorkflowProjection(ctx, *session)
		if !model.IsAssistantWorkstation(proposal.TargetWorkstation) || !assistantWorkstationAvailable(workflow, proposal.TargetWorkstation) {
			return nil, errors.New("assistant workstation is not available for the current project state")
		}
		session.ActiveWorkstation = proposal.TargetWorkstation
		session.WorkstationTitle = assistantWorkstationTitle(proposal.TargetWorkstation)
		proposal.ExecutionResult = map[string]any{"workstation": proposal.TargetWorkstation, "navigationOnly": true}
	case model.AssistantProposalContinueWithWebpageEvidence:
		projectID := firstNonEmptyString(session.Context.ProjectID, session.Configuration.AnalysisProjectID)
		if projectID == "" {
			return nil, errors.New("analysis project is missing")
		}
		workflow := s.assistantWorkflowProjection(ctx, *session)
		if workflow.Status != "source_binding_blocked" {
			return nil, errors.New("page-only source decision is unavailable for the current project state")
		}
		assessment, err := s.GetSourceBinding(ctx, projectID)
		if err != nil {
			return nil, err
		}
		state, err := s.DecideSourceBinding(ctx, projectID, SourceBindingDecisionRequest{Decision: "continue_page_only", AssessmentHash: assessment.AssessmentHash, IdempotencyKey: proposal.IdempotencyKey})
		if err != nil {
			return nil, err
		}
		proposal.ExecutionResult = map[string]any{"projectID": state.ProjectID, "sourceMode": "page_only", "uploadApproved": false}
	case model.AssistantProposalSelectProjectSource, model.AssistantProposalSelectLocalProject, model.AssistantProposalConnectGitHub,
		model.AssistantProposalAttachRequirementDocument, model.AssistantProposalAttachBrandAsset,
		model.AssistantProposalStoreDemoCredential:
		proposal.ExecutionResult = map[string]any{"clientActionRequired": true, "action": proposal.Kind}
	default:
		return nil, errors.New("unsupported assistant proposal kind")
	}
	return nil, nil
}

func invalidateAssistantAnalysisContext(session *model.AssistantSession) {
	if session == nil {
		return
	}
	session.Context.ProjectID = ""
	session.ActiveWorkstation = model.AssistantWorkstationOverview
	session.WorkstationTitle = assistantWorkstationTitle(model.AssistantWorkstationOverview)
	session.WorkstationStatus = "Configuration 已变更，等待重新分析"
}

func configurationFollowUpMessage(next model.ProjectConfigurationDraft, now time.Time) *model.AssistantMessage {
	if next.Readiness == "ready" {
		return &model.AssistantMessage{
			ID: fmt.Sprintf("agent_ready_%d", now.UnixNano()), Role: "agent", Kind: "proposal",
			Text: "配置已完整。确认后会在本机读取项目并生成三合一执行包草稿，不会上传。", CreatedAt: now,
			TargetWorkstation: model.AssistantWorkstationOverview,
			Proposals:         []model.AssistantProposal{newAssistantProposal(model.AssistantProposalConfirmConfiguration, "确认配置并生成方案", "锁定当前配置，在本机完成分析和执行包草稿；上传仍需单独审批。", next.Version, now, nil, model.AssistantWorkstationApproval)},
		}
	}
	if containsAssistantString(next.MissingFields, "sources") {
		return &model.AssistantMessage{
			ID: fmt.Sprintf("agent_sources_%d", now.UnixNano()), Role: "agent", Kind: "proposal",
			Text: "还差一个项目来源。请选择本地目录或 GitHub 仓库；两种方式只会保存安全引用。", CreatedAt: now,
			TargetWorkstation: model.AssistantWorkstationOverview,
			Proposals:         []model.AssistantProposal{newAssistantProposal(model.AssistantProposalSelectProjectSource, "选择项目来源", "从本地目录或 GitHub HTTPS 仓库中选择一种。", next.Version, now, nil, model.AssistantWorkstationOverview)},
		}
	}
	return nil
}

func refreshAssistantPendingStatus(session *model.AssistantSession) {
	for messageIndex := range session.Messages {
		for proposalIndex := range session.Messages[messageIndex].Proposals {
			if session.Messages[messageIndex].Proposals[proposalIndex].Status == "available" {
				session.Status = "awaiting_confirmation"
				session.WorkstationStatus = "等待确认下一步"
				return
			}
		}
	}
	session.Status = "waiting_for_user"
	session.WorkstationStatus = "已处理提案"
	refreshAssistantNextAction(session)
}

func refreshAssistantNextAction(session *model.AssistantSession) {
	if session == nil {
		return
	}
	for messageIndex := len(session.Messages) - 1; messageIndex >= 0; messageIndex-- {
		for proposalIndex := range session.Messages[messageIndex].Proposals {
			proposal := &session.Messages[messageIndex].Proposals[proposalIndex]
			if proposal.Status != "available" {
				continue
			}
			session.NextAction = model.AssistantNextAction{
				Kind: string(proposal.Kind), Title: proposal.Title, Description: proposal.Description,
				PrimaryLabel: assistantProposalPrimaryLabel(proposal.Kind), ProposalID: proposal.ID,
				TargetWorkstation: proposal.TargetWorkstation, RequiresUserAction: true,
				MissingFields: append([]string(nil), session.Configuration.MissingFields...),
			}
			return
		}
	}
	if session.Configuration.Confirmed && session.Configuration.AnalysisProjectID != "" {
		target := session.ActiveWorkstation
		if !model.IsAssistantWorkstation(target) {
			target = model.AssistantWorkstationApproval
		}
		session.NextAction = model.AssistantNextAction{Kind: "review_local_draft", Title: "检查本地生成结果", Description: "查看证据、录制方案和风险；确认配置不会自动上传。", PrimaryLabel: "查看生成结果", TargetWorkstation: target}
		return
	}
	if session.Configuration.Readiness == "ready" {
		session.NextAction = model.AssistantNextAction{Kind: "confirm_configuration", Title: "确认配置并生成方案", Description: "在本机分析项目并生成三合一草稿，不会上传。", PrimaryLabel: "确认并生成方案", TargetWorkstation: model.AssistantWorkstationOverview, RequiresUserAction: true}
		return
	}
	session.NextAction = model.AssistantNextAction{Kind: "provide_configuration", Title: "补全演示目标", Description: "告诉 Cascade 产品地址、目标受众和必须展示的业务流程。", PrimaryLabel: "继续对话", TargetWorkstation: model.AssistantWorkstationOverview, RequiresUserAction: true, MissingFields: append([]string(nil), session.Configuration.MissingFields...)}
}

func assistantProposalPrimaryLabel(kind model.AssistantProposalKind) string {
	switch kind {
	case model.AssistantProposalConfigurationPatch:
		return "应用字段变更"
	case model.AssistantProposalSelectProjectSource:
		return "选择项目来源"
	case model.AssistantProposalSelectLocalProject:
		return "选择本地目录"
	case model.AssistantProposalConnectGitHub:
		return "连接 GitHub"
	case model.AssistantProposalAttachRequirementDocument:
		return "选择需求文档"
	case model.AssistantProposalAttachBrandAsset:
		return "选择品牌素材"
	case model.AssistantProposalStoreDemoCredential:
		return "安全保存账号"
	case model.AssistantProposalConfirmConfiguration, model.AssistantProposalStartLocalAnalysis:
		return "确认并生成方案"
	case model.AssistantProposalContinueWithWebpageEvidence:
		return "仅使用网页证据继续"
	default:
		return "继续"
	}
}

func dismissStaleAssistantProposals(session *model.AssistantSession, currentProposalID string, currentVersion int64) {
	for messageIndex := range session.Messages {
		for proposalIndex := range session.Messages[messageIndex].Proposals {
			proposal := &session.Messages[messageIndex].Proposals[proposalIndex]
			if proposal.ID != currentProposalID && proposal.Status == "available" && proposal.BaseVersion != currentVersion {
				proposal.Status = "dismissed"
				proposal.ExecutionResult = map[string]any{"stale": true, "currentConfigurationVersion": currentVersion}
			}
		}
	}
}

func dismissAssistantProposalsOfKinds(session *model.AssistantSession, currentProposalID string, kinds ...model.AssistantProposalKind) {
	allowed := map[model.AssistantProposalKind]bool{}
	for _, kind := range kinds {
		allowed[kind] = true
	}
	for messageIndex := range session.Messages {
		for proposalIndex := range session.Messages[messageIndex].Proposals {
			proposal := &session.Messages[messageIndex].Proposals[proposalIndex]
			if proposal.ID != currentProposalID && proposal.Status == "available" && allowed[proposal.Kind] {
				proposal.Status = "dismissed"
				proposal.ExecutionResult = map[string]any{"superseded": true}
			}
		}
	}
}

func containsAssistantString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *Service) userInputFromConfiguration(draft model.ProjectConfigurationDraft) (orchestrator.UserInput, error) {
	input := orchestrator.UserInput{
		Mode: model.AppModeDesktop, ProductURL: draft.ProductURL, ProductDescription: draft.Objective,
		TargetAudience: draft.TargetAudience, TargetDurationSec: draft.TargetDurationSec, BrandTone: draft.BrandTone,
		MustShow: append([]string{}, draft.MustShow...), MustNotShow: append([]string{}, draft.MustNotShow...),
		ForbiddenPages: append([]string{}, draft.ForbiddenPages...), ForbiddenData: append([]string{}, draft.ForbiddenData...),
		AllowedDomains: append([]string{}, draft.AllowedDomains...),
	}
	for _, source := range draft.Sources {
		switch source.Kind {
		case "github_repository":
			if input.GitRepoURL == "" {
				input.GitRepoURL = source.URL
			}
		case "local_repository":
			resolved, ok := s.ResolveLocalSourceRef(source.Ref)
			if !ok {
				return input, fmt.Errorf("local source ref is unavailable: %s", source.Ref)
			}
			input.LocalRepoPath = resolved.Path
		case "requirement_document":
			resolved, ok := s.ResolveLocalSourceRef(source.Ref)
			if !ok {
				return input, fmt.Errorf("requirement source ref is unavailable: %s", source.Ref)
			}
			input.RequirementDocuments = append(input.RequirementDocuments, model.RequirementDocumentInput{ID: source.Ref, Kind: "local_file", Title: source.Label, LocalPath: resolved.Path})
		}
	}
	for _, ref := range draft.CredentialRefs {
		const prefix = "credential://demo/"
		if !strings.HasPrefix(ref, prefix) {
			continue
		}
		credential, err := credentialstore.ReadDemoCredential(strings.TrimPrefix(ref, prefix))
		if err != nil {
			return input, errors.New("demo credential ref is unavailable")
		}
		input.DemoUsername = credential.Username
		input.DemoPassword = credential.Password
		break
	}
	return input, nil
}

func findAssistantProposal(session *model.AssistantSession, proposalID string) *model.AssistantProposal {
	for messageIndex := range session.Messages {
		for proposalIndex := range session.Messages[messageIndex].Proposals {
			proposal := &session.Messages[messageIndex].Proposals[proposalIndex]
			if proposal.ID == proposalID {
				return proposal
			}
		}
	}
	return nil
}

func applyConfigurationPatch(current model.ProjectConfigurationDraft, patch model.ProjectConfigurationPatch) (model.ProjectConfigurationDraft, error) {
	patch = sanitizeConfigurationPatch(patch)
	if patch.ProjectName != nil {
		current.ProjectName = strings.TrimSpace(*patch.ProjectName)
	}
	if patch.ProductURL != nil {
		current.ProductURL = strings.TrimSpace(*patch.ProductURL)
	}
	if patch.Sources != nil {
		current.Sources = append([]model.ConfigurationSourceRef{}, (*patch.Sources)...)
	}
	if patch.Objective != nil {
		current.Objective = strings.TrimSpace(*patch.Objective)
	}
	if patch.TargetAudience != nil {
		current.TargetAudience = strings.TrimSpace(*patch.TargetAudience)
	}
	if patch.TargetDurationSec != nil {
		current.TargetDurationSec = *patch.TargetDurationSec
	}
	if patch.MustShow != nil {
		current.MustShow = cleanStrings(*patch.MustShow)
	}
	if patch.MustNotShow != nil {
		current.MustNotShow = cleanStrings(*patch.MustNotShow)
	}
	if patch.ForbiddenPages != nil {
		current.ForbiddenPages = cleanStrings(*patch.ForbiddenPages)
	}
	if patch.ForbiddenData != nil {
		current.ForbiddenData = cleanStrings(*patch.ForbiddenData)
	}
	if patch.BrandTone != nil {
		current.BrandTone = strings.TrimSpace(*patch.BrandTone)
	}
	if patch.CredentialRefs != nil {
		current.CredentialRefs = cleanStrings(*patch.CredentialRefs)
	}
	if patch.AllowedDomains != nil {
		current.AllowedDomains = cleanStrings(*patch.AllowedDomains)
	}
	if current.ProductURL != "" {
		parsed, err := url.Parse(current.ProductURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
			return current, errors.New("product URL must be an HTTP(S) URL")
		}
		if len(current.AllowedDomains) == 0 {
			current.AllowedDomains = []string{parsed.Hostname()}
		}
	}
	if current.TargetDurationSec != 0 && (current.TargetDurationSec < 5 || current.TargetDurationSec > 600) {
		return current, errors.New("target duration must be between 5 and 600 seconds")
	}
	current.Version++
	current.Confirmed = false
	current.ConfirmedAt = nil
	current.AnalysisProjectID = ""
	refreshConfigurationMetadata(&current)
	return current, nil
}

func sanitizeConfigurationPatch(patch model.ProjectConfigurationPatch) model.ProjectConfigurationPatch {
	if patch.ProjectName != nil {
		value := redactAssistantText(*patch.ProjectName)
		patch.ProjectName = &value
	}
	if patch.Objective != nil {
		value := redactAssistantText(*patch.Objective)
		patch.Objective = &value
	}
	if patch.TargetAudience != nil {
		value := redactAssistantText(*patch.TargetAudience)
		patch.TargetAudience = &value
	}
	if patch.BrandTone != nil {
		value := redactAssistantText(*patch.BrandTone)
		patch.BrandTone = &value
	}
	redactList := func(values *[]string) *[]string {
		if values == nil {
			return nil
		}
		cleaned := make([]string, 0, len(*values))
		for _, value := range *values {
			if safe := strings.TrimSpace(redactAssistantText(value)); safe != "" {
				cleaned = append(cleaned, safe)
			}
		}
		return &cleaned
	}
	patch.MustShow = redactList(patch.MustShow)
	patch.MustNotShow = redactList(patch.MustNotShow)
	patch.ForbiddenPages = redactList(patch.ForbiddenPages)
	patch.ForbiddenData = redactList(patch.ForbiddenData)
	if patch.Sources != nil {
		values := make([]model.ConfigurationSourceRef, 0, len(*patch.Sources))
		for _, source := range *patch.Sources {
			source.Label = redactAssistantText(source.Label)
			if source.Ref == "" && source.Kind != "github_repository" {
				continue
			}
			if strings.Contains(source.Ref, `:\`) || strings.HasPrefix(source.Ref, "/") {
				continue
			}
			values = append(values, source)
		}
		patch.Sources = &values
	}
	return patch
}

func configurationPatchEmpty(patch model.ProjectConfigurationPatch) bool {
	return patch.ProjectName == nil && patch.ProductURL == nil && patch.Sources == nil && patch.Objective == nil &&
		patch.TargetAudience == nil && patch.TargetDurationSec == nil && patch.MustShow == nil && patch.MustNotShow == nil &&
		patch.ForbiddenPages == nil && patch.ForbiddenData == nil && patch.BrandTone == nil && patch.CredentialRefs == nil && patch.AllowedDomains == nil
}

func newConfigurationDraft() model.ProjectConfigurationDraft {
	draft := model.ProjectConfigurationDraft{Version: 1, TargetDurationSec: 60}
	refreshConfigurationMetadata(&draft)
	return draft
}

func (s *Service) configurationFromProject(project *model.ProjectContext) model.ProjectConfigurationDraft {
	draft := model.ProjectConfigurationDraft{
		ProjectName: project.Name, ProductURL: project.ProductURL, Objective: project.ProductDescription,
		TargetAudience: project.TargetAudience, TargetDurationSec: 60, MustShow: append([]string{}, project.MustShow...),
		MustNotShow: append([]string{}, project.MustNotShow...), ForbiddenPages: append([]string{}, project.ForbiddenPages...),
		ForbiddenData: append([]string{}, project.ForbiddenData...), BrandTone: project.BrandTone, Version: 1,
	}
	if project.AccessPolicy != nil {
		draft.AllowedDomains = append([]string{}, project.AccessPolicy.AllowedDomains...)
	}
	for _, repository := range projectRepositories(project) {
		if repository.LocalPath != "" {
			ref, err := s.RegisterLocalSource("local_repository", repository.LocalPath)
			if err == nil {
				draft.Sources = append(draft.Sources, model.ConfigurationSourceRef{Ref: ref.Ref, Kind: ref.Kind, Label: ref.Label})
			}
			continue
		}
		if repository.URL != "" {
			draft.Sources = append(draft.Sources, model.ConfigurationSourceRef{Kind: "github_repository", Label: repositoryLabel(repository.URL), URL: repository.URL})
		}
	}
	refreshConfigurationMetadata(&draft)
	return draft
}

func projectRepositories(project *model.ProjectContext) []model.RepositoryInput {
	if project == nil {
		return nil
	}
	repositories := []model.RepositoryInput{}
	if project.Inputs != nil {
		repositories = append(repositories, project.Inputs.Repositories...)
	}
	if len(repositories) == 0 && project.LocalRepoPath != "" {
		repositories = append(repositories, model.RepositoryInput{LocalPath: project.LocalRepoPath, Provider: "local", ReadOnly: true})
	}
	if len(repositories) == 0 && project.GitRepoURL != "" {
		repositories = append(repositories, model.RepositoryInput{URL: project.GitRepoURL, Provider: "github", ReadOnly: true})
	}
	return repositories
}

func repositoryLabel(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "GitHub repository"
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return "GitHub repository"
	}
	return strings.TrimSuffix(parts[len(parts)-1], ".git")
}

func refreshConfigurationMetadata(draft *model.ProjectConfigurationDraft) {
	draft.MissingFields = configurationMissingFields(*draft)
	if len(draft.MissingFields) == 0 {
		draft.Readiness = "ready"
	} else {
		draft.Readiness = "incomplete"
	}
	copy := *draft
	copy.Hash = ""
	copy.MissingFields = nil
	data, _ := json.Marshal(copy)
	digest := sha256.Sum256(data)
	draft.Hash = "sha256:" + hex.EncodeToString(digest[:])
}

func configurationMissingFields(draft model.ProjectConfigurationDraft) []string {
	missing := []string{}
	if strings.TrimSpace(draft.ProjectName) == "" {
		missing = append(missing, "projectName")
	}
	if strings.TrimSpace(draft.ProductURL) == "" {
		missing = append(missing, "productURL")
	}
	if strings.TrimSpace(draft.Objective) == "" {
		missing = append(missing, "objective")
	}
	if strings.TrimSpace(draft.TargetAudience) == "" {
		missing = append(missing, "targetAudience")
	}
	if len(draft.Sources) == 0 {
		missing = append(missing, "sources")
	}
	return missing
}

func normalizeAssistantSession(session *model.AssistantSession) *model.AssistantSession {
	if session == nil {
		return nil
	}
	if !model.IsAssistantWorkstation(session.ActiveWorkstation) {
		session.ActiveWorkstation = model.AssistantWorkstationOverview
	}
	if session.ProcessedIdempotency == nil {
		session.ProcessedIdempotency = map[string]string{}
	}
	if session.Configuration.Version == 0 {
		session.Configuration.Version = 1
	}
	dismissDuplicateAssistantProposals(session)
	refreshConfigurationMetadata(&session.Configuration)
	refreshAssistantNextAction(session)
	return session
}

func dismissDuplicateAssistantProposals(session *model.AssistantSession) {
	if session == nil {
		return
	}
	seen := map[string]bool{}
	for messageIndex := len(session.Messages) - 1; messageIndex >= 0; messageIndex-- {
		for proposalIndex := len(session.Messages[messageIndex].Proposals) - 1; proposalIndex >= 0; proposalIndex-- {
			proposal := &session.Messages[messageIndex].Proposals[proposalIndex]
			if proposal.Status != "available" {
				continue
			}
			key := fmt.Sprintf("%s|%s|%d", proposal.Kind, proposal.TargetWorkstation, proposal.BaseVersion)
			if seen[key] {
				proposal.Status = "dismissed"
				proposal.ExecutionResult = map[string]any{"duplicate": true}
				continue
			}
			seen[key] = true
		}
	}
}

func assistantWorkstationTitle(workstation model.AssistantWorkstation) string {
	labels := map[model.AssistantWorkstation]string{
		model.AssistantWorkstationOverview: "项目配置", model.AssistantWorkstationEvidence: "本地分析",
		model.AssistantWorkstationPlan: "演示方案", model.AssistantWorkstationApproval: "执行审批",
		model.AssistantWorkstationExecution: "执行进度", model.AssistantWorkstationRepair: "修复",
		model.AssistantWorkstationAssets: "成品审核", model.AssistantWorkstationEditor: "视频编辑器",
	}
	return labels[workstation]
}

func assistantID(surface model.AssistantSurface, scope string) string {
	digest := sha256.Sum256([]byte(string(surface) + ":" + scope))
	return "assistant_" + hex.EncodeToString(digest[:12])
}

func redactAssistantText(value string) string {
	value = assistantSecretPattern.ReplaceAllString(value, "$1: [redacted]")
	return windowsAbsolutePathPattern.ReplaceAllString(value, "[local-path]")
}

func firstHTTPURL(value string) string {
	for _, field := range strings.Fields(value) {
		candidate := strings.Trim(field, "，。,.!?！？()[]{}<>\"'")
		parsed, err := url.Parse(candidate)
		if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" {
			return candidate
		}
	}
	return ""
}

func cleanStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(redactAssistantText(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
