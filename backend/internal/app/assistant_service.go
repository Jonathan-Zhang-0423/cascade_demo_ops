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
	assistantProjectNamePattern = regexp.MustCompile(`(?:项目名(?:称)?\s*[：:]\s*|项目名(?:叫|为|是)|项目名称(?:为|是)|命名为)\s*[“"']?([^，,。；;\n”"']{1,80})`)
	assistantAudiencePattern    = regexp.MustCompile(`(?:目标受众|受众)\s*[：:]\s*([^。；;\n]{1,160})|面向\s*([^，,。；;\n]{1,80}?)(?:的?\s*\d{1,3}\s*秒|制作|打造|，|,|。|；|;|$)`)
	assistantDurationPattern    = regexp.MustCompile(`(\d{1,3})\s*秒`)
	assistantMustShowPattern    = regexp.MustCompile(`(?:重点展示|必须展示|需要展示)\s*([^。；;\n]{1,200})`)
	assistantStepPattern        = regexp.MustCompile(`(?m)^\s*(?:第?[一二三四五六七八九十\d]+步[：:、.]?|\d+[.、)])\s*(.+?)\s*$`)
	assistantHTTPURLPattern     = regexp.MustCompile(`https?://[^\s，。；;、<>"']+`)
	assistantCorruptionPattern  = regexp.MustCompile(`\?{3,}`)
)

type assistantModelResponse struct {
	Reply            string                          `json:"reply"`
	Patch            model.ProjectConfigurationPatch `json:"patch"`
	HasPatch         bool                            `json:"hasPatch"`
	MissingFields    []string                        `json:"missingFields"`
	SuggestedActions []assistantSuggestedAction      `json:"suggestedActions"`
}

type assistantSuggestedAction struct {
	Kind              model.AssistantProposalKind `json:"kind"`
	Title             string                      `json:"title"`
	Description       string                      `json:"description"`
	TargetWorkstation model.AssistantWorkstation  `json:"targetWorkstation,omitempty"`
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
	if request.ProjectID != "" {
		if state, err := s.LoadProject(ctx, request.ProjectID); err == nil && state.ProjectContext != nil {
			draft = configurationFromProject(state.ProjectContext)
		}
	}
	session := &model.AssistantSession{
		ID: id, Context: request, Status: "waiting_for_user",
		ActiveWorkstation: model.AssistantWorkstationOverview,
		WorkstationTitle:  "项目配置", WorkstationStatus: "等待你的目标",
		Configuration: draft,
		Messages: []model.AssistantMessage{{
			ID: "welcome", Role: "agent", Kind: "answer",
			Text:      "告诉我你想为哪个产品制作演示、面向谁，以及必须展示什么。我会先整理成可审阅的 configuration，不会自动上传或执行。",
			CreatedAt: now,
		}},
		ProcessedIdempotency: map[string]string{},
	}
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
	response := s.generateAssistantResponse(ctx, session.Configuration, message)
	if len(request.SelectedSources) > 0 || len(request.CredentialRefs) > 0 {
		patch, patchErr := s.patchForSafeSelections(session.Configuration, request.SelectedSources, request.CredentialRefs)
		if patchErr != nil {
			return nil, patchErr
		}
		response = assistantModelResponse{Reply: "安全选择已完成。请确认将 opaque refs 写入 configuration；文件内容和凭据不会进入聊天。", Patch: patch, HasPatch: true}
	}
	proposals := s.proposalsFromModelResponse(session, response, now)
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
		CreatedAt: now, TargetWorkstation: model.AssistantWorkstationOverview, Proposals: proposals,
	})
	event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()), SessionID: session.ID, Type: "message", Text: text, CreatedAt: now}
	session.Events = append(session.Events, event)
	session.LastEventID = event.ID
	if request.IdempotencyKey != "" {
		session.ProcessedIdempotency[request.IdempotencyKey] = event.ID
	}
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

func (s *Service) generateAssistantResponse(ctx context.Context, draft model.ProjectConfigurationDraft, message string) assistantModelResponse {
	response := assistantFallbackResponse(message)
	if s.llm == nil {
		return response
	}
	configurationJSON, _ := json.Marshal(draft)
	var generated assistantModelResponse
	_, err := s.llm.GenerateJSON(ctx, config.ModelTaskPlanning, llm.JSONRequest{
		System:     "你是 DemoOps 项目配置 Agent。只提取用户明确提供的信息，不猜测凭据、路径或业务事实。你只能返回 configuration patch 和受限建议动作，不能执行命令、访问 URL、启动分析或上传。不要在 reply 中复述密码、token、完整本地路径或源码。",
		User:       fmt.Sprintf("当前 configuration：%s\n用户消息：%s", configurationJSON, message),
		SchemaName: "demoops_assistant_configuration_proposal_v1", MaxTokens: 1800, Temperature: 0.1,
		ResponseHint: "reply:string, hasPatch:boolean, patch:ProjectConfigurationPatch, missingFields:string[], suggestedActions:{kind,title,description,targetWorkstation}[]。kind 只能是 configuration_patch/select_local_project/connect_github/attach_requirement_document/attach_brand_asset/store_demo_credential/confirm_configuration/start_local_analysis/open_workstation/continue_with_webpage_evidence。Sources 不能包含本地绝对路径或 secret。",
	}, &generated)
	if err != nil {
		return response
	}
	generated.Patch = mergeExplicitAssistantPatch(sanitizeConfigurationPatch(generated.Patch), assistantFallbackResponse(message).Patch)
	// Source and credential refs are created only by trusted picker/credential paths.
	generated.Patch.Sources = nil
	generated.Patch.CredentialRefs = nil
	generated.Reply = redactAssistantText(generated.Reply)
	return generated
}

func assistantFallbackResponse(message string) assistantModelResponse {
	patch := model.ProjectConfigurationPatch{}
	hasPatch := false
	if candidate := firstHTTPURL(message); candidate != "" {
		patch.ProductURL = &candidate
		hasPatch = true
	}
	objective := strings.TrimSpace(redactAssistantText(message))
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
		value := strings.TrimSpace(firstNonEmptyString(match[1], match[2]))
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
	} else if matches := assistantStepPattern.FindAllStringSubmatch(message, -1); len(matches) > 0 {
		steps := make([]string, 0, len(matches))
		for _, match := range matches {
			if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
				steps = append(steps, strings.TrimSpace(match[1]))
			}
		}
		if len(steps) > 0 {
			patch.MustShow = &steps
			hasPatch = true
		}
	}
	return assistantModelResponse{
		Reply: "我会把这轮信息整理为 configuration 变更。确认前不会保存，也不会启动分析。",
		Patch: patch, HasPatch: hasPatch,
	}
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

func (s *Service) proposalsFromModelResponse(session *model.AssistantSession, response assistantModelResponse, now time.Time) []model.AssistantProposal {
	proposals := make([]model.AssistantProposal, 0, len(response.SuggestedActions)+1)
	hasPatch := response.HasPatch && !configurationPatchEmpty(response.Patch)
	if hasPatch {
		proposals = append(proposals, newAssistantProposal(model.AssistantProposalConfigurationPatch, "确认 configuration 变更", "应用本轮提取的字段；不会启动分析或上传。", session.Configuration.Version, now, &response.Patch, ""))
	}
	for _, action := range response.SuggestedActions {
		if !model.IsAssistantProposalKind(action.Kind) || action.Kind == model.AssistantProposalConfigurationPatch {
			continue
		}
		if hasPatch && (action.Kind == model.AssistantProposalConfirmConfiguration || action.Kind == model.AssistantProposalStartLocalAnalysis) {
			continue
		}
		if !model.IsAssistantWorkstation(action.TargetWorkstation) {
			action.TargetWorkstation = model.AssistantWorkstationOverview
		}
		proposals = append(proposals, newAssistantProposal(action.Kind, redactAssistantText(action.Title), redactAssistantText(action.Description), session.Configuration.Version, now, nil, action.TargetWorkstation))
	}
	if len(proposals) == 0 && session.Configuration.Readiness == "ready" && !session.Configuration.Confirmed {
		proposals = append(proposals, newAssistantProposal(model.AssistantProposalConfirmConfiguration, "确认 configuration 并开始本地分析", "锁定当前摘要并启动本地代码与需求分析；执行包上传仍需单独审批。", session.Configuration.Version, now, nil, model.AssistantWorkstationEvidence))
	}
	return proposals
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
	return session, s.assistantStore.Save(ctx, session)
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
	session.Status = "waiting_for_user"
	session.WorkstationStatus = "已处理提案"
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
		proposal.ExecutionResult = map[string]any{"configurationVersion": next.Version, "configurationHash": next.Hash}
		if next.Readiness == "ready" {
			now := time.Now().UTC()
			return &model.AssistantMessage{
				ID: fmt.Sprintf("agent_ready_%d", now.UnixNano()), Role: "agent", Kind: "proposal",
				Text: "Configuration 已完整。确认后只会启动本地分析；执行包上传仍需单独审批。", CreatedAt: now,
				TargetWorkstation: model.AssistantWorkstationOverview,
				Proposals:         []model.AssistantProposal{newAssistantProposal(model.AssistantProposalConfirmConfiguration, "确认并开始本地分析", "锁定当前 configuration 并分析项目；不会上传执行包。", next.Version, now, nil, model.AssistantWorkstationEvidence)},
			}, nil
		}
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
			return nil, err
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
		session.ActiveWorkstation = model.AssistantWorkstationEvidence
		session.WorkstationTitle = "本地分析"
		proposal.ExecutionResult = map[string]any{"projectID": state.ProjectID, "analysisStarted": true, "uploadApproved": false}
	case model.AssistantProposalOpenWorkstation:
		if model.IsAssistantWorkstation(proposal.TargetWorkstation) {
			session.ActiveWorkstation = proposal.TargetWorkstation
			session.WorkstationTitle = assistantWorkstationTitle(proposal.TargetWorkstation)
		}
	case model.AssistantProposalContinueWithWebpageEvidence:
		projectID := firstNonEmptyString(session.Context.ProjectID, session.Configuration.AnalysisProjectID)
		if projectID == "" {
			return nil, errors.New("analysis project is missing")
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
	case model.AssistantProposalSelectLocalProject, model.AssistantProposalConnectGitHub,
		model.AssistantProposalAttachRequirementDocument, model.AssistantProposalAttachBrandAsset,
		model.AssistantProposalStoreDemoCredential:
		proposal.ExecutionResult = map[string]any{"clientActionRequired": true, "action": proposal.Kind}
	default:
		return nil, errors.New("unsupported assistant proposal kind")
	}
	return nil, nil
}

func (s *Service) userInputFromConfiguration(draft model.ProjectConfigurationDraft) (orchestrator.UserInput, error) {
	if field := corruptedConfigurationField(draft); field != "" {
		return orchestrator.UserInput{}, fmt.Errorf("configuration contains corrupted text in %s; re-enter this field before analysis", field)
	}
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

func configurationFromProject(project *model.ProjectContext) model.ProjectConfigurationDraft {
	draft := model.ProjectConfigurationDraft{
		ProjectName: project.Name, ProductURL: project.ProductURL, Objective: project.ProductDescription,
		TargetAudience: project.TargetAudience, TargetDurationSec: 60, MustShow: append([]string{}, project.MustShow...),
		MustNotShow: append([]string{}, project.MustNotShow...), ForbiddenPages: append([]string{}, project.ForbiddenPages...),
		ForbiddenData: append([]string{}, project.ForbiddenData...), BrandTone: project.BrandTone, Version: 1,
	}
	if project.AccessPolicy != nil {
		draft.AllowedDomains = append([]string{}, project.AccessPolicy.AllowedDomains...)
	}
	if project.GitRepoURL != "" {
		draft.Sources = append(draft.Sources, model.ConfigurationSourceRef{Kind: "github_repository", Label: project.GitRepoURL, URL: project.GitRepoURL})
	}
	refreshConfigurationMetadata(&draft)
	return draft
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
	refreshConfigurationMetadata(&session.Configuration)
	return session
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
	candidate := strings.TrimRight(assistantHTTPURLPattern.FindString(value), ").!?！？]}")
	parsed, err := url.Parse(candidate)
	if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" {
		return candidate
	}
	return ""
}

func corruptedConfigurationField(draft model.ProjectConfigurationDraft) string {
	fields := []struct {
		name   string
		values []string
	}{
		{name: "projectName", values: []string{draft.ProjectName}},
		{name: "objective", values: []string{draft.Objective}},
		{name: "targetAudience", values: []string{draft.TargetAudience}},
		{name: "mustShow", values: draft.MustShow},
		{name: "mustNotShow", values: draft.MustNotShow},
		{name: "forbiddenData", values: draft.ForbiddenData},
	}
	for _, field := range fields {
		for _, value := range field.values {
			if assistantCorruptionPattern.MatchString(value) {
				return field.name
			}
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
