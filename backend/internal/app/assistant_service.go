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
	"cascade-demoops/backend/internal/store"
)

var (
	assistantSecretPattern      = regexp.MustCompile(`(?i)(password|passwd|密码|口令|token|令牌|secret|密钥|api[_-]?key)(?:\s*[:=：]\s*|\s*(?:是|为)\s*|\s*)[^\s,，;；。]+`)
	windowsAbsolutePathPattern  = regexp.MustCompile(`(?i)\b[A-Z]:\\[^\r\n，,。；;、<>:"|?*]+`)
	assistantProjectNamePattern = regexp.MustCompile(`(?:项目名(?:称)?\s*[：:]\s*|项目名(?:叫|为|是)|项目名称(?:为|是)|命名为|新建项目\s*)\s*[“"']?([^，,。；;\n”"']{1,80})`)
	assistantAudiencePattern    = regexp.MustCompile(`(?:目标受众|受众)\s*[：:]\s*([^。；;\n]{1,160})|面向\s*([^，,。；;\n]{1,80}?)(?:的?\s*\d{1,3}\s*秒|制作|打造|，|,|。|；|;|$)`)
	assistantDurationPattern    = regexp.MustCompile(`(\d{1,3})\s*秒`)
	assistantMustShowPattern    = regexp.MustCompile(`(?:重点展示|必须展示|需要展示)\s*([^。；;\n]{1,200})`)
	assistantObjectivePattern   = regexp.MustCompile(`(?:演示目标|目标|需求)(?:改为|修改为|是|为)\s*[“"']?([^。；;\n”"']{1,500})`)
	assistantStepPattern        = regexp.MustCompile(`(?m)^\s*(?:第?[一二三四五六七八九十\d]+步[：:、.]?|\d+[.、)])\s*(.+?)\s*$`)
	assistantHTTPURLPattern     = regexp.MustCompile(`https?://[^\s，。；;、<>"']+`)
	assistantBareDomainPattern  = regexp.MustCompile(`(?i)(?:项目地址|产品地址|网址|url|^|[\s，,；;：:])\s*([a-z0-9](?:[a-z0-9-]{0,62}\.)+[a-z]{2,}(?:/[^\s，。；;、<>"']*)?)`)
	assistantCorruptionPattern  = regexp.MustCompile(`\?{3,}`)
)

type assistantModelResponse struct {
	Reply            string                          `json:"reply"`
	Patch            model.ProjectConfigurationPatch `json:"patch"`
	HasPatch         bool                            `json:"hasPatch"`
	MissingFields    []string                        `json:"missingFields"`
	Clarification    *model.AssistantQuestion        `json:"clarification,omitempty"`
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
	ProjectAttached        bool                             `json:"projectAttached"`
	Stage                  string                           `json:"stage"`
	Status                 string                           `json:"status"`
	AvailableWorkstations  []model.AssistantWorkstation     `json:"availableWorkstations"`
	AvailableActions       []model.AgentActionSpecification `json:"availableActions,omitempty"`
	RecommendedWorkstation model.AssistantWorkstation       `json:"recommendedWorkstation"`
	UploadApprovalRequired bool                             `json:"uploadApprovalRequired"`
	ResultReviewRequired   bool                             `json:"resultReviewRequired"`
}

func assistantCopy(locale, english, chinese string) string {
	if locale == "zh-CN" {
		return chinese
	}
	return english
}

func (s *Service) CreateAssistantSession(ctx context.Context, request model.AssistantContext) (*model.AssistantSession, error) {
	if request.Surface != model.AssistantSurfaceProjects && request.Surface != model.AssistantSurfaceRepositories {
		return nil, errors.New("unsupported assistant surface")
	}
	if strings.TrimSpace(request.ScopeKey) == "" {
		return nil, errors.New("assistant scope key is required")
	}
	if request.CreateProjectOnFirstTurn {
		if request.Surface != model.AssistantSurfaceProjects || strings.TrimSpace(request.ProjectID) == "" {
			return nil, errors.New("new project conversations require a projects surface and project ID")
		}
		if request.ScopeKey != "project_"+request.ProjectID {
			return nil, errors.New("new project conversations require a canonical project scope")
		}
	}
	id := assistantID(request.Surface, request.ScopeKey)
	if request.Locale != "zh-CN" {
		request.Locale = "en-US"
	}
	if existing, err := s.assistantStore.Load(ctx, id); err == nil {
		messageCount := len(existing.Messages)
		if existing.Context.Locale == "" {
			existing.Context.Locale = request.Locale
		}
		normalized := normalizeAssistantSession(existing)
		if len(normalized.Messages) != messageCount || existing.Context.Locale != "" {
			_ = s.assistantStore.Save(ctx, normalized)
		}
		return normalized, nil
	}
	now := time.Now().UTC()
	draft := newConfigurationDraft()
	activeWorkstation := model.AssistantWorkstationOverview
	workstationTitle := assistantCopy(request.Locale, "Project brief", "项目配置")
	workstationStatus := assistantCopy(request.Locale, "Waiting for your goal", "等待你的目标")
	if request.ProjectID != "" {
		if state, err := s.LoadProject(ctx, request.ProjectID); err == nil && state.ProjectContext != nil {
			if state.ProjectContext.AssistantSessionID == "" {
				state.ProjectContext.AssistantSessionID = id
				state.ProjectContext.UpdatedAt = time.Now().UTC()
				_ = s.states.Save(ctx, state)
			}
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
		Configuration:        draft,
		Messages:             []model.AssistantMessage{},
		ProcessedIdempotency: map[string]string{},
	}
	if question := nextAssistantQuestionForLocale(draft, request.Locale); question != nil {
		session.PendingQuestion = question
		session.Messages = append(session.Messages, model.AssistantMessage{ID: "welcome", Role: "agent", Kind: "answer", Text: question.Prompt, Question: question, CreatedAt: now})
	} else {
		text := assistantCopy(request.Locale, "Your project context is ready. Tell me what you’d like to inspect, change, or continue.", "项目上下文已恢复。告诉我你想检查、调整或继续哪一步。")
		session.Messages = append(session.Messages, model.AssistantMessage{ID: "welcome", Role: "agent", Kind: "answer", Text: text, CreatedAt: now})
	}
	if agentActionsV2Enabled() {
		session.ActionMode = assistantActionModeV2
	}
	refreshAssistantNextAction(session)
	refreshAssistantActionState(session)
	return session, s.assistantStore.Save(ctx, session)
}

func (s *Service) GetAssistantSession(ctx context.Context, sessionID string) (*model.AssistantSession, error) {
	session, err := s.assistantStore.Load(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messageCount := len(session.Messages)
	normalized := normalizeAssistantSession(session)
	interrupted := false
	if normalized.ActiveTurn != nil && (normalized.ActiveTurn.Status == "queued" || normalized.ActiveTurn.Status == "running") {
		s.assistantRunsMu.Lock()
		_, running := s.assistantRuns[sessionID]
		s.assistantRunsMu.Unlock()
		if !running {
			now := time.Now().UTC()
			normalized.ActiveTurn.Status = "failed"
			normalized.ActiveTurn.CompletedAt = &now
			normalized.ActiveTurn.FailureReason = assistantCopy(normalized.Context.Locale,
				"Cascade restarted before this response finished. Your message is saved—retry whenever you’re ready.",
				"Cascade 在回复完成前重新启动了。你的消息已保存，随时可以重试。")
			normalized.Status = "failed"
			event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()), SessionID: normalized.ID, Type: "turn_interrupted", Text: normalized.ActiveTurn.FailureReason, CreatedAt: now}
			normalized.Events = append(normalized.Events, event)
			normalized.LastEventID = event.ID
			interrupted = true
		}
	}
	if len(normalized.Messages) != messageCount || interrupted {
		_ = s.assistantStore.Save(ctx, normalized)
	}
	return normalized, nil
}

func (s *Service) ensureAssistantDraftProject(ctx context.Context, session *model.AssistantSession, message string, now time.Time) error {
	if session == nil || !session.Context.CreateProjectOnFirstTurn {
		return nil
	}
	projectID := strings.TrimSpace(session.Context.ProjectID)
	if projectID == "" {
		return errors.New("new project conversations require a project ID")
	}
	if state, err := s.LoadProject(ctx, projectID); err == nil {
		if state == nil || state.ProjectContext == nil {
			return errors.New("conversation project state is incomplete")
		}
		if session.Configuration.ProjectName == "" {
			session.Configuration.ProjectName = state.ProjectContext.Name
			refreshConfigurationMetadata(&session.Configuration)
		}
		state.ProjectContext.AssistantSessionID = session.ID
		state.ProjectContext.UpdatedAt = now
		if state.ProjectContext.Inputs == nil {
			state.ProjectContext.Inputs = &model.ProjectInputBundle{}
		}
		if strings.TrimSpace(state.ProjectContext.Inputs.RawUserPrompt) == "" {
			state.ProjectContext.Inputs.RawUserPrompt = message
		}
		if err := s.states.Save(ctx, state); err != nil {
			return err
		}
		return nil
	} else if !errors.Is(err, store.ErrStateNotFound) {
		return err
	}

	name := assistantDraftProjectName(message)
	session.Configuration.ProjectName = name
	refreshConfigurationMetadata(&session.Configuration)
	session.Context.ProjectName = name
	project := &model.ProjectContext{
		ID:                 projectID,
		SchemaVersion:      model.ProjectContextSchemaVersion,
		Mode:               model.AppModeDesktop,
		Name:               name,
		AssistantSessionID: session.ID,
		ProductDescription: message,
		Inputs:             &model.ProjectInputBundle{RawUserPrompt: message},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	return s.states.Save(ctx, &orchestrator.CascadeState{
		ProjectID:      projectID,
		CurrentNode:    orchestrator.NodeInputCtx,
		Status:         orchestrator.FlowStatusCreated,
		ProjectContext: project,
	})
}

func assistantDraftProjectName(message string) string {
	if rawURL := firstHTTPURL(message); rawURL != "" {
		if parsed, err := url.Parse(rawURL); err == nil && parsed.Hostname() != "" {
			host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
			parts := strings.Split(host, ".")
			if len(parts) > 0 && parts[0] != "" {
				return strings.ToUpper(parts[0][:1]) + parts[0][1:] + " demo"
			}
		}
	}
	cleaned := strings.Join(strings.Fields(strings.ReplaceAll(message, "\n", " ")), " ")
	for _, prefix := range []string{"please ", "create a demo for ", "create a product demo for ", "make a demo for ", "make a product demo for ", "show "} {
		if strings.HasPrefix(strings.ToLower(cleaned), prefix) {
			cleaned = strings.TrimSpace(cleaned[len(prefix):])
			break
		}
	}
	words := strings.Fields(cleaned)
	if len(words) > 8 {
		words = words[:8]
	}
	cleaned = strings.Trim(strings.Join(words, " "), " .,:;!?-—")
	if cleaned == "" {
		return "New demo project"
	}
	runes := []rune(cleaned)
	if len(runes) > 56 {
		cleaned = strings.TrimSpace(string(runes[:56]))
	}
	return cleaned
}

func assistantConversationDraft(state *orchestrator.CascadeState) bool {
	return state != nil && state.Status == orchestrator.FlowStatusCreated && state.ProjectIntelligence == nil && state.UnderstandingReport == nil && state.WorkflowGraph == nil && state.ExecutableScriptBundle == nil && state.DesktopCloudRun == nil
}

func (s *Service) assistantDraftMayAutosave(ctx context.Context, session *model.AssistantSession) bool {
	if session == nil || !session.Context.CreateProjectOnFirstTurn || session.Configuration.Confirmed || session.Context.ProjectID == "" {
		return false
	}
	state, err := s.LoadProject(ctx, session.Context.ProjectID)
	return err == nil && assistantConversationDraft(state)
}

func (s *Service) syncAssistantDraftProject(ctx context.Context, session *model.AssistantSession, now time.Time) error {
	if session == nil || !session.Context.CreateProjectOnFirstTurn || session.Context.ProjectID == "" {
		return nil
	}
	state, err := s.LoadProject(ctx, session.Context.ProjectID)
	if err != nil {
		return err
	}
	if !assistantConversationDraft(state) || state.ProjectContext == nil {
		return nil
	}
	project := state.ProjectContext
	if session.Configuration.ProjectName != "" {
		project.Name = session.Configuration.ProjectName
		session.Context.ProjectName = project.Name
	}
	project.ProductURL = session.Configuration.ProductURL
	if session.Configuration.Objective != "" {
		project.ProductDescription = session.Configuration.Objective
	}
	project.TargetAudience = session.Configuration.TargetAudience
	project.BrandTone = session.Configuration.BrandTone
	project.MustShow = append([]string{}, session.Configuration.MustShow...)
	project.MustNotShow = append([]string{}, session.Configuration.MustNotShow...)
	project.ForbiddenPages = append([]string{}, session.Configuration.ForbiddenPages...)
	project.ForbiddenData = append([]string{}, session.Configuration.ForbiddenData...)
	if project.Inputs == nil {
		project.Inputs = &model.ProjectInputBundle{}
	}
	if project.Inputs.Metadata == nil {
		project.Inputs.Metadata = map[string]any{}
	}
	project.Inputs.Metadata["target_duration_sec"] = session.Configuration.TargetDurationSec
	project.Inputs.Repositories = assistantRepositoriesFromSources(session.Configuration.Sources)
	project.UpdatedAt = now
	return s.states.Save(ctx, state)
}

func assistantRepositoriesFromSources(sources []model.ConfigurationSourceRef) []model.RepositoryInput {
	repositories := make([]model.RepositoryInput, 0, len(sources))
	for _, source := range sources {
		repository := model.RepositoryInput{ReadOnly: true, Primary: len(repositories) == 0}
		switch source.Kind {
		case "github_repository":
			repository.Kind = "github"
			repository.Provider = "github"
			repository.URL = source.URL
		case "local_project":
			repository.Kind = "local"
			repository.Provider = "local"
			repository.LocalPath = source.Label
		default:
			continue
		}
		repositories = append(repositories, repository)
	}
	return repositories
}

func (s *Service) SubmitAssistantTurn(ctx context.Context, sessionID string, request model.AssistantTurnRequest) (*model.AssistantSession, error) {
	rawMessage := strings.TrimSpace(request.Message)
	if rawMessage == "" {
		return nil, errors.New("assistant message is required")
	}
	if len(rawMessage) > 6000 {
		return nil, errors.New("assistant message is too long")
	}
	message := redactAssistantText(rawMessage)
	now := time.Now().UTC()

	s.assistantMu.Lock()
	session, err := s.GetAssistantSession(ctx, sessionID)
	if err != nil {
		s.assistantMu.Unlock()
		return nil, err
	}
	if request.IdempotencyKey != "" {
		if _, ok := session.ProcessedIdempotency[request.IdempotencyKey]; ok {
			s.assistantMu.Unlock()
			return session, nil
		}
	}
	if session.ActiveTurn != nil && (session.ActiveTurn.Status == "queued" || session.ActiveTurn.Status == "running") {
		s.assistantMu.Unlock()
		return nil, errors.New("Cascade is already responding in this project")
	}
	if err := s.ensureAssistantDraftProject(ctx, session, message, now); err != nil {
		s.assistantMu.Unlock()
		return nil, err
	}
	userMessageID := fmt.Sprintf("user_%d", now.UnixNano())
	turnID := fmt.Sprintf("turn_%d", now.UnixNano())
	session.Status = "thinking"
	session.Messages = append(session.Messages, model.AssistantMessage{
		ID: userMessageID, Role: "user", Kind: "answer", Text: message, CreatedAt: now,
	})
	session.ActiveTurn = &model.AssistantActiveTurn{
		ID: turnID, UserMessageID: userMessageID, Status: "queued", IdempotencyKey: request.IdempotencyKey, StartedAt: now,
	}
	event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()), SessionID: session.ID, Type: "turn_queued", Text: assistantCopy(session.Context.Locale, "Cascade is working in the background", "Cascade 正在后台处理"), CreatedAt: now}
	session.Events = append(session.Events, event)
	session.LastEventID = event.ID
	if request.IdempotencyKey != "" {
		session.ProcessedIdempotency[request.IdempotencyKey] = turnID
	}
	if err := s.assistantStore.Save(ctx, session); err != nil {
		s.assistantMu.Unlock()
		return nil, err
	}
	s.assistantMu.Unlock()

	runContext, cancel := context.WithCancel(context.Background())
	s.assistantRunsMu.Lock()
	s.assistantRuns[sessionID] = cancel
	s.assistantRunsMu.Unlock()
	go func() {
		defer func() {
			s.assistantRunsMu.Lock()
			delete(s.assistantRuns, sessionID)
			s.assistantRunsMu.Unlock()
		}()
		_, runErr := s.processAssistantTurn(runContext, sessionID, request)
		s.finishAssistantTurn(sessionID, turnID, runErr)
	}()
	return session, nil
}

// SubmitAssistantTurnAndWait preserves the synchronous service contract for
// internal callers that need the completed turn. Desktop HTTP uses the durable
// enqueueing method above so navigation never owns the lifetime of a response.
func (s *Service) SubmitAssistantTurnAndWait(ctx context.Context, sessionID string, request model.AssistantTurnRequest) (*model.AssistantSession, error) {
	if _, err := s.SubmitAssistantTurn(ctx, sessionID, request); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		session, err := s.GetAssistantSession(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		if session.ActiveTurn == nil || (session.ActiveTurn.Status != "queued" && session.ActiveTurn.Status != "running") {
			if session.ActiveTurn != nil && session.ActiveTurn.Status == "failed" {
				return session, errors.New(session.ActiveTurn.FailureReason)
			}
			return session, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) processAssistantTurn(ctx context.Context, sessionID string, request model.AssistantTurnRequest) (*model.AssistantSession, error) {
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	rawMessage := strings.TrimSpace(request.Message)
	if rawMessage == "" {
		return nil, errors.New("assistant message is required")
	}
	if len(rawMessage) > 6000 {
		return nil, errors.New("assistant message is too long")
	}
	message := redactAssistantText(rawMessage)
	session, err := s.GetAssistantSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	prepared := session.ActiveTurn != nil && (session.ActiveTurn.Status == "queued" || session.ActiveTurn.Status == "running") && (request.IdempotencyKey == "" || session.ActiveTurn.IdempotencyKey == request.IdempotencyKey)
	if request.IdempotencyKey != "" && !prepared {
		if _, ok := session.ProcessedIdempotency[request.IdempotencyKey]; ok {
			return session, nil
		}
	}
	detectedLocalSources := s.detectAssistantLocalSources(rawMessage)
	now := time.Now().UTC()
	if err := s.ensureAssistantDraftProject(ctx, session, message, now); err != nil {
		return nil, err
	}
	session.Status = "thinking"
	if prepared {
		session.ActiveTurn.Status = "running"
	} else {
		session.Messages = append(session.Messages, model.AssistantMessage{
			ID: fmt.Sprintf("user_%d", now.UnixNano()), Role: "user", Kind: "answer", Text: message, CreatedAt: now,
		})
	}
	if proposal, source, issue := assistantGitHubSourceTurn(session, rawMessage); proposal != nil {
		if issue != "" {
			text := issue + " Paste a public repository URL like https://github.com/owner/repository, or tell me if you’d rather use a local folder."
			session.Messages = append(session.Messages, model.AssistantMessage{ID: fmt.Sprintf("agent_github_help_%d", now.UnixNano()+1), Role: "agent", Kind: "answer", Text: text, CreatedAt: now})
			session.Status = "waiting_for_user"
			event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()), SessionID: session.ID, Type: "message", Text: text, CreatedAt: now}
			session.Events = append(session.Events, event)
			session.LastEventID = event.ID
			if request.IdempotencyKey != "" {
				session.ProcessedIdempotency[request.IdempotencyKey] = event.ID
			}
			refreshAssistantNextAction(session)
			refreshAssistantActionState(session)
			return session, s.assistantStore.Save(ctx, session)
		}
		followUps, completeErr := s.completeAssistantClientActionInSession(ctx, session, proposal, model.AssistantClientActionResultRequest{
			BaseVersion: session.Configuration.Version, SelectedSources: []model.ConfigurationSourceRef{*source},
		}, now)
		if completeErr != nil {
			return nil, completeErr
		}
		if err := s.syncAssistantDraftProject(ctx, session, now); err != nil {
			return nil, err
		}
		acknowledgement := fmt.Sprintf("Got it—I connected %s as this project’s source. I’ll use that repository as context; private access, if needed, still stays in local GitHub authorization.", source.Label)
		session.Messages = append(session.Messages, model.AssistantMessage{ID: fmt.Sprintf("agent_github_connected_%d", now.UnixNano()+1), Role: "agent", Kind: "answer", Text: acknowledgement, CreatedAt: now})
		session.Messages = append(session.Messages, followUps...)
		event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()), SessionID: session.ID, Type: "client_action", Text: string(proposal.Kind) + ": completed in conversation", CreatedAt: now}
		session.Events = append(session.Events, event)
		session.LastEventID = event.ID
		if request.IdempotencyKey != "" {
			session.ProcessedIdempotency[request.IdempotencyKey] = event.ID
		}
		refreshAssistantPendingStatus(session)
		refreshAssistantNextAction(session)
		refreshAssistantActionState(session)
		return session, s.assistantStore.Save(ctx, session)
	}
	if resolved, resolveErr := s.resolveNaturalAssistantDecision(ctx, session, message, request.IdempotencyKey, now); resolved || resolveErr != nil {
		return session, resolveErr
	}
	dismissSupersededAssistantProposals(session, message)
	if session.Context.ProjectID != "" {
		_, _ = s.AppendProjectActivity(ctx, model.ProjectActivityEvent{
			ProjectID: session.Context.ProjectID, RunID: session.ID, Mode: model.ProjectCanvasModeAct,
			Kind: "assistant_action", Status: model.ProjectActivityRunning,
			Title:  assistantCopy(session.Context.Locale, "Cascade is shaping the next step", "Cascade 正在整理下一步"),
			Detail: assistantCopy(session.Context.Locale, "Reviewing the approved project context and preparing the next safe action.", "正在读取已批准的项目上下文并准备安全动作摘要。"),
		})
	}
	workflow := s.assistantWorkflowProjection(ctx, *session)
	response := s.generateAssistantResponseForLocale(ctx, session.Configuration, workflow, message, session.Context.Locale)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if session.PendingQuestion != nil {
		questionPatch := applyQuestionAnswer(model.ProjectConfigurationPatch{}, *session.PendingQuestion, message)
		if session.PendingQuestion.Field == "productURL" && strings.TrimSpace(message) == firstHTTPURL(message) {
			// A bare URL answers the URL question; it is not an objective or project name.
			response.Patch = questionPatch
		} else {
			response.Patch = applyQuestionAnswer(response.Patch, *session.PendingQuestion, message)
		}
		response.HasPatch = response.HasPatch || !configurationPatchEmpty(response.Patch)
		if response.HasPatch {
			response.Reply = assistantCopy(session.Context.Locale, "Here’s what I understood. Confirm it naturally, or tell me what to change.", "这是我理解到的内容。你可以自然地确认，或告诉我需要修改什么。")
		}
	}
	trustedSelection := len(request.SelectedSources) > 0 || len(request.CredentialRefs) > 0
	if trustedSelection {
		patch, patchErr := s.patchForSafeSelections(session.Configuration, request.SelectedSources, request.CredentialRefs)
		if patchErr != nil {
			return nil, patchErr
		}
		response = assistantModelResponse{Reply: "安全选择已完成。要把这些安全引用加入项目吗？直接回复继续，或告诉我需要调整什么；文件内容和凭据不会进入聊天。", Patch: patch, HasPatch: true}
	}
	responseQuestion := sanitizeAssistantClarification(response.Clarification)
	autoSaved := response.HasPatch && !trustedSelection && s.assistantDraftMayAutosave(ctx, session)
	if autoSaved {
		next, patchErr := applyConfigurationPatch(session.Configuration, response.Patch)
		if patchErr != nil {
			return nil, patchErr
		}
		session.Configuration = next
		response.Patch = model.ProjectConfigurationPatch{}
		response.HasPatch = false
		workflow = s.assistantWorkflowProjection(ctx, *session)
		if err := s.syncAssistantDraftProject(ctx, session, now); err != nil {
			return nil, err
		}
		session.PendingQuestion = nil
		if responseQuestion != nil {
			session.PendingQuestion = responseQuestion
			response.Reply = responseQuestion.Prompt
		} else if next.Readiness == "ready" {
			response.Reply = assistantCopy(session.Context.Locale, "I have enough context to prepare this project. Would you like me to continue?", "我已经有足够的背景来准备这个项目。要继续吗？")
		} else if question := nextAssistantQuestionForLocale(next, session.Context.Locale); question != nil {
			responseQuestion = question
			session.PendingQuestion = question
			response.Reply = question.Prompt
		} else if containsAssistantString(next.MissingFields, "sources") {
			response.Reply = assistantCopy(session.Context.Locale, "Where should I learn about the product from? Choose a local folder or paste a GitHub repository URL.", "我应该从哪里了解产品？你可以选择本地文件夹，或粘贴 GitHub 仓库链接。")
			response.SuggestedActions = mergeAssistantActions([]assistantSuggestedAction{{
				Kind: model.AssistantProposalSelectProjectSource, Title: "Choose a product source",
				Description:       "Select a local folder or GitHub repository. Cascade stores only a safe reference in the conversation.",
				TargetWorkstation: model.AssistantWorkstationOverview,
			}}, response.SuggestedActions)
		}
	} else if session.ActionMode == assistantActionModeV2 && response.HasPatch && !configurationPatchEmpty(response.Patch) {
		next, patchErr := applyConfigurationPatch(session.Configuration, response.Patch)
		if patchErr != nil {
			return nil, patchErr
		}
		session.Configuration = next
		invalidateAssistantAnalysisContext(session)
		response.HasPatch = false
		response.Patch = model.ProjectConfigurationPatch{}
		response.Reply = assistantCopy(session.Context.Locale, "I saved the low-risk project details. Local sources and demo credentials still use secure controls.", "已自动整理低风险配置字段。本地来源和演示凭据仍会通过安全控件单独收集。")
	}
	if responseQuestion != nil && session.PendingQuestion == nil {
		session.PendingQuestion = responseQuestion
		response.Reply = responseQuestion.Prompt
	}
	proposals := s.proposalsFromModelResponse(session, workflow, response, now)
	attachDetectedAssistantLocalSources(proposals, detectedLocalSources)
	text := strings.TrimSpace(redactAssistantText(response.Reply))
	if text == "" {
		text = "I’ve organized what you shared. Reply naturally to continue, pause, or change my understanding."
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
		CreatedAt: now, TargetWorkstation: model.AssistantWorkstationOverview, Proposals: proposals, Question: responseQuestion,
	})
	if followUp, autoErr := s.autoStartAssistantAnalysisIfReady(ctx, session, now.Add(time.Nanosecond)); autoErr != nil {
		return nil, autoErr
	} else if followUp != nil {
		session.Messages = append(session.Messages, *followUp)
	}
	refreshAssistantNextAction(session)
	refreshAssistantActionState(session)
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
	if session.Context.ProjectID != "" {
		activityStatus := model.ProjectActivityCompleted
		title := assistantCopy(session.Context.Locale, "Cascade finished this step", "Cascade 已完成这一步")
		if len(proposals) > 0 {
			activityStatus = model.ProjectActivityWaitingForApproval
			title = assistantCopy(session.Context.Locale, "Waiting for your confirmation", "等待你的确认")
		}
		_, _ = s.AppendProjectActivity(ctx, model.ProjectActivityEvent{
			ProjectID: session.Context.ProjectID, RunID: session.ID, Mode: model.ProjectCanvasModeAct,
			Kind: "assistant_action", Status: activityStatus, Title: title,
			Detail: assistantCopy(session.Context.Locale, "Reply naturally to continue, pause, or tell me what to change.", "你可以直接回复继续、暂不，或说明需要修改的内容。"),
		})
	}
	return session, nil
}

func (s *Service) finishAssistantTurn(sessionID, turnID string, runErr error) {
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	ctx := context.Background()
	session, err := s.GetAssistantSession(ctx, sessionID)
	if err != nil || session.ActiveTurn == nil || session.ActiveTurn.ID != turnID || session.ActiveTurn.Status == "cancelled" {
		return
	}
	now := time.Now().UTC()
	session.ActiveTurn.CompletedAt = &now
	eventType := "turn_completed"
	eventText := "Cascade finished this response"
	if errors.Is(runErr, context.Canceled) {
		session.ActiveTurn.Status = "cancelled"
		session.Status = "waiting_for_user"
		eventType = "turn_cancelled"
		eventText = assistantCopy(session.Context.Locale, "Response stopped. Your message is still saved.", "回复已停止。你的消息仍然保留。")
	} else if runErr != nil {
		session.ActiveTurn.Status = "failed"
		session.ActiveTurn.FailureReason = assistantCopy(session.Context.Locale,
			"Cascade could not finish this response. Your message is saved and can be retried.",
			"Cascade 未能完成这次回复。你的消息已保存，可以重试。")
		session.Status = "failed"
		eventType = "turn_failed"
		eventText = session.ActiveTurn.FailureReason
	} else {
		session.ActiveTurn.Status = "completed"
	}
	event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()), SessionID: session.ID, Type: eventType, Text: eventText, CreatedAt: now}
	session.Events = append(session.Events, event)
	session.LastEventID = event.ID
	_ = s.assistantStore.Save(ctx, session)
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
		Text:             "已将手动填写内容整理为同一套受控 configuration 提案。直接回复继续，或告诉我哪部分需要修改。",
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
	refreshAssistantActionState(session)
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
	return s.generateAssistantResponseForLocale(ctx, draft, workflow, message, "en-US")
}

func (s *Service) generateAssistantResponseForLocale(ctx context.Context, draft model.ProjectConfigurationDraft, workflow assistantLLMWorkflow, message, locale string) assistantModelResponse {
	response := assistantFallbackResponse(draft, message)
	if locale == "zh-CN" {
		response.Reply = "我会把这些信息整理成清晰的项目更新，供你确认。"
	}
	workflowActions := deterministicWorkflowActions(message, workflow)
	if len(workflowActions) > 0 {
		response.Patch = model.ProjectConfigurationPatch{}
		response.HasPatch = false
		response.Reply = assistantCopy(locale, "I found the relevant project view. Ask me to open it; switching views will not approve, upload, download, or publish anything.", "已找到当前项目中可访问的工作台。如果你想查看，直接告诉我继续；切换视图不会执行审批、上传、下载或审核。")
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
	systemPrompt := assistantCopy(locale,
		"You are Cascade, a DemoOps planning agent. Stay in one natural conversation and ask one focused question at a time. Reply only in English. Extract only facts the user explicitly provided; never invent credentials, paths, or business facts. Create and refine a concise 3–6 word projectName from the supplied context. When a consequential request is truly ambiguous, set clarification with 2–3 mutually exclusive guesses, best guess first and recommended=true; otherwise do not set clarification. Use only workflow.availableActions. Never execute commands, visit URLs, approve uploads, download assets, or submit reviews. Never repeat passwords, tokens, full local paths, source code, package contents, or raw errors. Connected sources deliberately hide their real refs and must not be requested again.",
		"你是 Cascade，一名 DemoOps 项目规划 Agent。始终保持自然对话，一次只问一个重点问题，并且只使用简体中文回复。只提取用户明确提供的信息，不猜测凭据、路径或业务事实。根据上下文生成并逐步改进简洁、具体的 3–6 个词项目名。只有会实质影响结果且确实存在歧义时，才设置 clarification，并提供 2–3 个互斥猜测，最佳猜测排在第一且 recommended=true。只能使用 workflow.availableActions 中的动作。不得执行命令、访问 URL、批准上传、下载资产或提交审核。不得复述密码、token、完整本地路径、源码、执行包内容或原始错误。已连接来源会隐藏真实引用，不得要求用户重复提供。")
	trace, err := s.llm.GenerateJSON(ctx, config.ModelTaskPlanning, llm.JSONRequest{
		System:     systemPrompt,
		User:       fmt.Sprintf("当前 configuration：%s\n当前 workflow：%s\n用户消息：%s", configurationJSON, workflowJSON, message),
		SchemaName: "demoops_assistant_configuration_proposal_v1", MaxTokens: 1800, Temperature: 0.1,
		ResponseHint: "reply:string, hasPatch:boolean, patch:ProjectConfigurationPatch, missingFields:string[], clarification?:{field,prompt,options:[{label,value,description,recommended}]}, suggestedActions:{kind,title,description,targetWorkstation}[]。clarification 仅在真正歧义时使用，options 必须有 2–3 个互斥选择且第一项为推荐项。kind 只能是 configuration_patch/select_local_project/connect_github/attach_requirement_document/attach_brand_asset/store_demo_credential/confirm_configuration/start_local_analysis/open_workstation/continue_with_webpage_evidence/retry_page_scan/regenerate_execution_package，且必须对应 workflow.availableActions。Sources 不能包含本地绝对路径或 secret。",
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
	generated.Clarification = sanitizeAssistantClarification(generated.Clarification)
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

func (s *Service) assistantWorkflowProjection(ctx context.Context, session model.AssistantSession) (workflow assistantLLMWorkflow) {
	workflow = assistantLLMWorkflow{
		Stage: "configuration", Status: "waiting_for_configuration",
		AvailableWorkstations:  []model.AssistantWorkstation{model.AssistantWorkstationOverview},
		RecommendedWorkstation: model.AssistantWorkstationOverview,
		UploadApprovalRequired: true, ResultReviewRequired: true,
	}
	defer func() {
		if session.ActionMode == assistantActionModeV2 {
			workflow.AvailableActions = assistantActionsAvailableForStage(workflow.Stage)
		}
	}()
	projectID := firstNonEmptyString(session.Context.ProjectID, session.Configuration.AnalysisProjectID)
	if projectID == "" {
		if session.Configuration.Readiness == "ready" {
			workflow.Status = "waiting_for_configuration_confirmation"
		}
		return workflow
	}
	workflow.ProjectAttached = true
	state, err := s.LoadProject(ctx, projectID)
	if err != nil || state == nil {
		workflow.Status = "project_state_unavailable"
		return workflow
	}
	if session.Context.CreateProjectOnFirstTurn && !session.Configuration.Confirmed {
		workflow.Stage = "configuration"
		workflow.Status = "configuration_changed"
		if assistantConversationDraft(state) {
			workflow.Status = "gathering_project_context"
		}
		if session.Configuration.Readiness == "ready" {
			workflow.Status = "waiting_for_configuration_confirmation"
		}
		return workflow
	}
	workflow.Stage = "local_analysis"
	workflow.Status = "analysis_started"
	workflow.AvailableWorkstations = appendAssistantWorkstation(workflow.AvailableWorkstations, model.AssistantWorkstationEvidence)
	workflow.RecommendedWorkstation = model.AssistantWorkstationEvidence
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
	} else if strings.TrimSpace(draft.Objective) == "" && assistantMessageContainsTaskIntent(message) && (assistantObjectiveFallbackAllowed(message) || assistantMessageContainsDemoBrief(message)) {
		objective = strings.TrimSpace(redactAssistantText(message))
	}
	if objective != "" && len([]rune(objective)) <= 500 {
		patch.Objective = &objective
		hasPatch = true
	}
	if match := assistantProjectNamePattern.FindStringSubmatch(message); len(match) > 1 {
		value := normalizeAssistantProjectName(match[1])
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
		value := splitAssistantIntentSteps(match[1])
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
	} else if steps := inferAssistantIntentSteps(message); len(steps) > 0 {
		patch.MustShow = &steps
		hasPatch = true
	}
	return assistantModelResponse{
		Reply: "I’ll organize that into a clear project update for you to review.",
		Patch: patch, HasPatch: hasPatch, SuggestedActions: deterministicAssistantActions(message),
	}
}

func assistantMessageContainsDemoBrief(message string) bool {
	normalized := strings.ToLower(message)
	return (strings.Contains(normalized, "演示") && strings.Contains(normalized, "制作")) ||
		(strings.Contains(normalized, "demo") && (strings.Contains(normalized, "create") || strings.Contains(normalized, "make")))
}

func applyQuestionAnswer(patch model.ProjectConfigurationPatch, question model.AssistantQuestion, message string) model.ProjectConfigurationPatch {
	value := strings.TrimSpace(redactAssistantText(message))
	if value == "" {
		return patch
	}
	switch question.Field {
	case "projectName":
		patch.ProjectName = &value
	case "productURL":
		if candidate := firstHTTPURL(value); candidate != "" {
			patch.ProductURL = &candidate
		}
	case "objective":
		patch.Objective = &value
	case "targetAudience":
		patch.TargetAudience = &value
	}
	return patch
}

func nextAssistantQuestion(draft model.ProjectConfigurationDraft) *model.AssistantQuestion {
	missing := map[string]bool{}
	for _, field := range draft.MissingFields {
		missing[field] = true
	}
	switch {
	case missing["productURL"]:
		return &model.AssistantQuestion{Field: "productURL", Prompt: "Which product should this demo cover? Share the product URL if you have it."}
	case missing["objective"]:
		return &model.AssistantQuestion{Field: "objective", Prompt: "What should someone understand or believe after watching this demo?"}
	case missing["targetAudience"]:
		return assistantAudienceQuestion(draft)
	case missing["projectName"]:
		return &model.AssistantQuestion{Field: "projectName", Prompt: "What should we call this demo project?"}
	default:
		return nil
	}
}

func nextAssistantQuestionForLocale(draft model.ProjectConfigurationDraft, locale string) *model.AssistantQuestion {
	question := nextAssistantQuestion(draft)
	if question == nil || locale != "zh-CN" {
		return question
	}
	localized := *question
	switch localized.Field {
	case "productURL":
		localized.Prompt = "这次演示要介绍哪个产品？如果有产品链接，请直接发给我。"
	case "objective":
		localized.Prompt = "看完演示后，你希望观众理解或相信什么？"
	case "targetAudience":
		localized.Prompt = "这次演示主要面向谁？你可以选择一个建议，也可以直接描述受众。"
		labels := []string{"潜在客户", "销售团队", "新用户"}
		descriptions := []string{"突出产品价值和最短的成功路径。", "便于在销售沟通和后续跟进中复用。", "优先呈现产品定位、首次成功和清晰步骤。"}
		for index := range localized.Options {
			if index < len(labels) {
				localized.Options[index].Label = labels[index]
				localized.Options[index].Value = labels[index]
				localized.Options[index].Description = descriptions[index]
			}
		}
	case "projectName":
		localized.Prompt = "这个演示项目应该叫什么？"
	}
	return &localized
}

func assistantAudienceQuestion(draft model.ProjectConfigurationDraft) *model.AssistantQuestion {
	type audienceGuess struct {
		label       string
		description string
		matches     []string
	}
	guesses := []audienceGuess{
		{label: "Prospective customers", description: "Lead with the product's value and the shortest convincing path to an outcome.", matches: []string{"customer", "buyer", "prospect", "客户", "采购"}},
		{label: "Sales teams", description: "Make the flow easy to reuse in pitches, discovery calls, and follow-ups.", matches: []string{"sales", "pitch", "revenue", "销售", "售前", "成交"}},
		{label: "New users", description: "Prioritize orientation, first success, and a clear step-by-step product journey.", matches: []string{"onboard", "getting started", "new user", "learn", "新用户", "上手", "入门"}},
	}
	context := strings.ToLower(strings.Join([]string{draft.Objective, draft.ProjectName, strings.Join(draft.MustShow, " ")}, " "))
	recommended := 0
	for index, guess := range guesses {
		if containsAnyAssistantText(context, guess.matches...) {
			recommended = index
			break
		}
	}
	ordered := append([]audienceGuess{guesses[recommended]}, append(append([]audienceGuess{}, guesses[:recommended]...), guesses[recommended+1:]...)...)
	options := make([]model.AssistantQuestionOption, 0, len(ordered))
	for index, guess := range ordered {
		options = append(options, model.AssistantQuestionOption{Label: guess.label, Value: guess.label, Description: guess.description, Recommended: index == 0})
	}
	return &model.AssistantQuestion{
		Field:   "targetAudience",
		Prompt:  "Who should this demo be designed for? My best guess is the first option, but you can choose another or describe the audience in your own words.",
		Options: options,
	}
}

func sanitizeAssistantClarification(question *model.AssistantQuestion) *model.AssistantQuestion {
	if question == nil {
		return nil
	}
	allowedFields := map[string]bool{"projectName": true, "productURL": true, "objective": true, "targetAudience": true, "targetDurationSec": true, "mustShow": true, "brandTone": true, "sourceKind": true}
	field := strings.TrimSpace(question.Field)
	prompt := strings.TrimSpace(redactAssistantText(question.Prompt))
	if !allowedFields[field] || prompt == "" || len([]rune(prompt)) > 320 {
		return nil
	}
	options := make([]model.AssistantQuestionOption, 0, 3)
	seen := map[string]bool{}
	for _, option := range question.Options {
		label := strings.TrimSpace(redactAssistantText(option.Label))
		value := strings.TrimSpace(redactAssistantText(firstNonEmptyString(option.Value, option.Label)))
		description := strings.TrimSpace(redactAssistantText(option.Description))
		key := strings.ToLower(value)
		if label == "" || value == "" || description == "" || seen[key] || len([]rune(label)) > 80 || len([]rune(value)) > 240 || len([]rune(description)) > 240 {
			continue
		}
		seen[key] = true
		options = append(options, model.AssistantQuestionOption{Label: label, Value: value, Description: description, Recommended: option.Recommended})
		if len(options) == 3 {
			break
		}
	}
	if len(options) < 2 {
		return nil
	}
	recommended := 0
	for index, option := range options {
		if option.Recommended {
			recommended = index
			break
		}
	}
	if recommended != 0 {
		options[0], options[recommended] = options[recommended], options[0]
	}
	for index := range options {
		options[index].Recommended = index == 0
	}
	return &model.AssistantQuestion{Field: field, Prompt: prompt, Options: options}
}

func assistantNaturalDecision(message string) string {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(message)), " .,!?:;。！？，：；")
	if len([]rune(normalized)) > 64 {
		return ""
	}
	confirm := map[string]bool{"yes": true, "yes please": true, "y": true, "confirm": true, "continue": true, "go ahead": true, "go for it": true, "proceed": true, "do it": true, "approve": true, "looks good": true, "sounds good": true, "that's right": true, "that is right": true, "correct": true, "ok": true, "okay": true, "确认": true, "继续": true, "可以": true, "好的": true, "就这样": true, "没问题": true}
	dismiss := map[string]bool{"no": true, "no thanks": true, "n": true, "cancel": true, "not now": true, "not yet": true, "later": true, "hold off": true, "don't": true, "do not": true, "取消": true, "不要": true, "暂不": true, "先不要": true, "稍后": true}
	if confirm[normalized] {
		return "confirmed"
	}
	if dismiss[normalized] {
		return "dismissed"
	}
	return ""
}

func latestAvailableAssistantProposals(session *model.AssistantSession) []*model.AssistantProposal {
	if session == nil {
		return nil
	}
	for messageIndex := len(session.Messages) - 1; messageIndex >= 0; messageIndex-- {
		proposals := make([]*model.AssistantProposal, 0, len(session.Messages[messageIndex].Proposals))
		for proposalIndex := range session.Messages[messageIndex].Proposals {
			proposal := &session.Messages[messageIndex].Proposals[proposalIndex]
			if proposal.Status == "available" {
				proposals = append(proposals, proposal)
			}
		}
		if len(proposals) > 0 {
			return proposals
		}
	}
	return nil
}

func latestAvailableAssistantProposal(session *model.AssistantSession) *model.AssistantProposal {
	proposals := latestAvailableAssistantProposals(session)
	if len(proposals) == 0 {
		return nil
	}
	return proposals[len(proposals)-1]
}

func proposalAcceptsNaturalDecision(kind model.AssistantProposalKind) bool {
	switch kind {
	case model.AssistantProposalConfigurationPatch, model.AssistantProposalConfirmConfiguration, model.AssistantProposalStartLocalAnalysis,
		model.AssistantProposalOpenWorkstation, model.AssistantProposalContinueWithWebpageEvidence,
		model.AssistantProposalRetryPageScan, model.AssistantProposalRegenerateExecutionPackage:
		return true
	default:
		return false
	}
}

func assistantProposalChoice(proposals []*model.AssistantProposal, message string) *model.AssistantProposal {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(message)), " .,!?:;。！？，：；")
	if normalized == "" {
		return nil
	}
	for _, proposal := range proposals {
		title := strings.ToLower(strings.TrimSpace(proposal.Title))
		if normalized == title || (len([]rune(title)) >= 6 && strings.Contains(normalized, title)) {
			return proposal
		}
	}
	for _, proposal := range proposals {
		matches := false
		switch proposal.Kind {
		case model.AssistantProposalRetryPageScan:
			matches = containsAnyAssistantText(normalized, "retry the page scan", "retry page scan", "scan again", "rescan", "重新扫描", "再扫描")
		case model.AssistantProposalRegenerateExecutionPackage:
			matches = containsAnyAssistantText(normalized, "regenerate", "rebuild the package", "重新生成", "重建执行包")
		case model.AssistantProposalContinueWithWebpageEvidence:
			matches = containsAnyAssistantText(normalized, "webpage evidence", "page only", "网页证据", "仅网页")
		}
		if matches {
			return proposal
		}
	}
	return nil
}

func assistantProposalChoiceQuestion(proposals []*model.AssistantProposal) *model.AssistantQuestion {
	options := make([]model.AssistantQuestionOption, 0, 3)
	for index, proposal := range proposals {
		if !proposalAcceptsNaturalDecision(proposal.Kind) || strings.TrimSpace(proposal.Title) == "" || strings.TrimSpace(proposal.Description) == "" {
			continue
		}
		options = append(options, model.AssistantQuestionOption{Label: proposal.Title, Value: proposal.Title, Description: proposal.Description, Recommended: index == 0})
		if len(options) == 3 {
			break
		}
	}
	if len(options) < 2 {
		return nil
	}
	options[0].Recommended = true
	return &model.AssistantQuestion{Field: "proposalChoice", Prompt: "I can take more than one reasonable next step. I’d start with the first option—which approach should I use?", Options: options}
}

func dismissSupersededAssistantProposals(session *model.AssistantSession, message string) {
	if session == nil || assistantNaturalDecision(message) != "" || assistantMessageAsksQuestion(message) {
		return
	}
	for _, proposal := range latestAvailableAssistantProposals(session) {
		if proposalAcceptsNaturalDecision(proposal.Kind) {
			proposal.Status = "dismissed"
		}
	}
}

func assistantMessageAsksQuestion(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	if strings.HasSuffix(normalized, "?") || strings.HasSuffix(normalized, "？") {
		return true
	}
	for _, prefix := range []string{"what ", "why ", "how ", "when ", "where ", "who ", "can ", "could ", "would ", "will ", "does ", "is ", "are ", "explain ", "tell me ", "什么", "为什么", "怎么", "如何", "能否", "可以解释"} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func (s *Service) resolveNaturalAssistantDecision(ctx context.Context, session *model.AssistantSession, message, idempotencyKey string, now time.Time) (bool, error) {
	decision := assistantNaturalDecision(message)
	available := latestAvailableAssistantProposals(session)
	proposals := make([]*model.AssistantProposal, 0, len(available))
	for _, proposal := range available {
		if proposalAcceptsNaturalDecision(proposal.Kind) {
			proposals = append(proposals, proposal)
		}
	}
	if len(proposals) == 0 {
		return false, nil
	}
	proposal := assistantProposalChoice(proposals, message)
	if proposal != nil {
		decision = "confirmed"
	} else if len(proposals) == 1 {
		proposal = proposals[0]
	} else if decision == "confirmed" {
		question := assistantProposalChoiceQuestion(proposals)
		if question == nil {
			return false, nil
		}
		session.PendingQuestion = question
		session.Messages = append(session.Messages, model.AssistantMessage{ID: fmt.Sprintf("agent_choice_%d", now.UnixNano()), Role: "agent", Kind: "answer", Text: question.Prompt, Question: question, CreatedAt: now})
		session.Status = "waiting_for_user"
		refreshAssistantNextAction(session)
		event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()+2), SessionID: session.ID, Type: "clarification", Text: "proposal choice requested", CreatedAt: now}
		session.Events = append(session.Events, event)
		session.LastEventID = event.ID
		if strings.TrimSpace(idempotencyKey) != "" {
			session.ProcessedIdempotency[idempotencyKey] = event.ID
		}
		return true, s.assistantStore.Save(ctx, session)
	} else if decision == "dismissed" {
		for _, candidate := range proposals {
			candidate.Status = "dismissed"
		}
		session.PendingQuestion = nil
		session.Messages = append(session.Messages, model.AssistantMessage{ID: fmt.Sprintf("agent_declined_%d", now.UnixNano()), Role: "agent", Kind: "answer", Text: "That’s fine—I haven’t changed anything. Tell me what you’d prefer whenever you’re ready.", CreatedAt: now})
		refreshAssistantPendingStatus(session)
		refreshAssistantActionState(session)
		event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()+2), SessionID: session.ID, Type: "proposal", Text: "proposal choices: dismissed", CreatedAt: now}
		session.Events = append(session.Events, event)
		session.LastEventID = event.ID
		if strings.TrimSpace(idempotencyKey) != "" {
			session.ProcessedIdempotency[idempotencyKey] = event.ID
		}
		return true, s.assistantStore.Save(ctx, session)
	}
	if decision == "" || proposal == nil {
		return false, nil
	}
	var followUp *model.AssistantMessage
	var err error
	if decision == "confirmed" {
		if proposal.BaseVersion != session.Configuration.Version {
			return true, fmt.Errorf("configuration version conflict: current=%d proposal=%d", session.Configuration.Version, proposal.BaseVersion)
		}
		followUp, err = s.executeAssistantProposal(ctx, session, proposal)
		if err != nil {
			return true, err
		}
	} else {
		followUp = &model.AssistantMessage{ID: fmt.Sprintf("agent_declined_%d", now.UnixNano()), Role: "agent", Kind: "answer", Text: "No changes were made. Tell me what you’d like to adjust.", CreatedAt: now}
	}
	proposal.Status = decision
	session.PendingQuestion = nil
	if followUp != nil {
		session.Messages = append(session.Messages, *followUp)
		if followUp.Question != nil {
			session.PendingQuestion = followUp.Question
		}
	}
	refreshAssistantPendingStatus(session)
	refreshAssistantNextAction(session)
	event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()+2), SessionID: session.ID, Type: "proposal", Text: string(proposal.Kind) + ": " + decision, CreatedAt: now}
	session.Events = append(session.Events, event)
	session.LastEventID = event.ID
	if strings.TrimSpace(idempotencyKey) != "" {
		session.ProcessedIdempotency[idempotencyKey] = event.ID
	}
	return true, s.assistantStore.Save(ctx, session)
}

// Generic attachment language (for example "需求文档") must not become a
// project objective. Keep the fallback broad for complete task descriptions,
// but require an explicit task signal when the same turn also contains safe
// picker/credential actions.
func assistantObjectiveFallbackAllowed(message string) bool {
	if len(deterministicAssistantActions(message)) == 0 {
		return true
	}
	normalized := strings.ToLower(message)
	return containsAnyAssistantText(normalized, "任务目标", "演示目标", "新建项目", "创建项目", "登录", "登陆", "演示输出", "等待agent", "等待 agent")
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
		add(model.AssistantProposalConnectGitHub, "Connect a GitHub repository", "Paste the public GitHub HTTPS URL in your next message. Private repository authorization stays local and never enters chat.")
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
	if strings.Contains(normalized, "测试账号") || strings.Contains(normalized, "测试用账号") || strings.Contains(normalized, "演示账号") || strings.Contains(normalized, "demo credential") || strings.Contains(normalized, "密码") {
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
		proposals = append(proposals, newAssistantProposal(model.AssistantProposalConfigurationPatch, "Use this understanding", "Review what Cascade understood. Applying it updates only the project draft; it does not analyze, execute, or upload anything.", session.Configuration.Version, now, &response.Patch, ""))
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
	if len(proposals) == 0 && session.ActionMode != assistantActionModeV2 && session.Configuration.Readiness == "ready" && !session.Configuration.Confirmed && !assistantProposalAlreadyAvailable(session, model.AssistantProposalConfirmConfiguration, "") {
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
	s.assistantRunsMu.Lock()
	cancel := s.assistantRuns[sessionID]
	s.assistantRunsMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.assistantMu.Lock()
	defer s.assistantMu.Unlock()
	session, err := s.GetAssistantSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	session.Status = "idle"
	if session.ActiveTurn != nil && (session.ActiveTurn.Status == "queued" || session.ActiveTurn.Status == "running") {
		now := time.Now().UTC()
		session.ActiveTurn.Status = "cancelled"
		session.ActiveTurn.CompletedAt = &now
		event := model.AssistantEvent{ID: fmt.Sprintf("%020d", now.UnixNano()), SessionID: session.ID, Type: "turn_cancelled", Text: "Cascade stopped. Your message remains in this conversation.", CreatedAt: now}
		session.Events = append(session.Events, event)
		session.LastEventID = event.ID
	}
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
	now := time.Now().UTC()
	followUps, err := s.completeAssistantClientActionInSession(ctx, session, proposal, request, now)
	if err != nil {
		return nil, err
	}
	session.Messages = append(session.Messages, followUps...)
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
	refreshAssistantActionState(session)
	if err := s.assistantStore.Save(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

func (s *Service) completeAssistantClientActionInSession(ctx context.Context, session *model.AssistantSession, proposal *model.AssistantProposal, request model.AssistantClientActionResultRequest, now time.Time) ([]model.AssistantMessage, error) {
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
	session.PendingQuestion = nil
	proposal.ExecutionResult = map[string]any{"configurationVersion": next.Version, "configurationHash": next.Hash, "clientActionCompleted": true}
	dismissStaleAssistantProposals(session, proposal.ID, next.Version)
	followUps := []model.AssistantMessage{}
	if session.ActionMode != assistantActionModeV2 {
		if followUp := configurationFollowUpMessage(next, now); followUp != nil {
			followUps = append(followUps, *followUp)
			if followUp.Question != nil {
				session.PendingQuestion = followUp.Question
			}
		}
	}
	if followUp, autoErr := s.autoStartAssistantAnalysisIfReady(ctx, session, now); autoErr != nil {
		return nil, autoErr
	} else if followUp != nil {
		followUps = append(followUps, *followUp)
		if followUp.Question != nil {
			session.PendingQuestion = followUp.Question
		}
	}
	return followUps, nil
}

func assistantGitHubSourceTurn(session *model.AssistantSession, message string) (*model.AssistantProposal, *model.ConfigurationSourceRef, string) {
	var proposal *model.AssistantProposal
	for _, candidate := range latestAvailableAssistantProposals(session) {
		if candidate.Kind == model.AssistantProposalConnectGitHub || candidate.Kind == model.AssistantProposalSelectProjectSource {
			proposal = candidate
			break
		}
	}
	if proposal == nil {
		return nil, nil, ""
	}
	candidate := firstHTTPURL(message)
	if candidate == "" {
		if containsAnyAssistantText(strings.ToLower(message), "github.com", "http://github", "https://github") {
			return proposal, nil, "That repository address looks incomplete."
		}
		return nil, nil, ""
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed == nil {
		return proposal, nil, "That GitHub repository address could not be read safely."
	}
	if !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		if strings.EqualFold(parsed.Hostname(), "github.com") || strings.Contains(strings.ToLower(candidate), "github.com") {
			return proposal, nil, "That GitHub address needs to use a clean HTTPS repository URL."
		}
		return nil, nil, ""
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return proposal, nil, "That GitHub address points to a page, not a repository."
	}
	owner, ownerErr := url.PathUnescape(parts[0])
	repository, repositoryErr := url.PathUnescape(parts[1])
	if ownerErr != nil || repositoryErr != nil || strings.ContainsAny(owner+repository, " \\") {
		return proposal, nil, "That GitHub repository address could not be read safely."
	}
	repository = strings.TrimSuffix(repository, ".git")
	if repository == "" {
		return proposal, nil, "That GitHub address is missing the repository name."
	}
	normalized := "https://github.com/" + owner + "/" + repository
	label := owner + "/" + repository
	return proposal, &model.ConfigurationSourceRef{Kind: "github_repository", Label: label, URL: normalized}, ""
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
	if status == "confirmed" {
		session.PendingQuestion = nil
	}
	if followUp != nil {
		session.Messages = append(session.Messages, *followUp)
		if followUp.Question != nil {
			session.PendingQuestion = followUp.Question
		}
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
		input.ProjectID = firstNonEmptyString(session.Context.ProjectID, session.Configuration.AnalysisProjectID)
		if session.ActionMode == assistantActionModeV2 && input.ProjectID == "" {
			input.ProjectID = "proj_" + shortAssistantDigest(session.ID+"|"+session.Configuration.Hash)
		}
		analysisStarted := time.Now()
		s.emitAssistantProgress(input.ProjectID, orchestrator.ProgressEvent{Level: orchestrator.ProgressLevelInfo, Message: "开始本地项目分析", Detail: "正在读取安全引用、网页证据和用户目标。"})
		analysisCtx := orchestrator.WithProgressSink(ctx, func(event orchestrator.ProgressEvent) {
			s.emitAssistantProgress(input.ProjectID, event)
		})
		state, err := s.CreateProject(analysisCtx, input)
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
			s.emitAssistantProgress(input.ProjectID, orchestrator.ProgressEvent{Level: orchestrator.ProgressLevelError, Message: "本地分析被阻断", Detail: info.Message, ElapsedMS: time.Since(analysisStarted).Milliseconds()})
			proposal.ExecutionResult = map[string]any{"projectID": state.ProjectID, "analysisStarted": true, "analysisBlocked": true, "errorCode": info.Code, "uploadApproved": false}
			repairProposals := []model.AssistantProposal{
				newAssistantProposal(model.AssistantProposalRetryPageScan, "重新扫描登录页", "重新运行登录页状态机并收集 Browser Scan 证据。", session.Configuration.Version, now, nil, model.AssistantWorkstationEvidence),
				newAssistantProposal(model.AssistantProposalRegenerateExecutionPackage, "重新生成执行包", "保留当前安全引用并重新执行本地分析与产包门禁。", session.Configuration.Version, now, nil, model.AssistantWorkstationRepair),
			}
			return &model.AssistantMessage{
				ID: fmt.Sprintf("agent_analysis_blocked_%d", now.UnixNano()), Role: "agent", Kind: "error",
				Text: info.Message + "。项目已保存在本机，可直接执行下方修复动作。", CreatedAt: now,
				TargetWorkstation: model.AssistantWorkstationEvidence, Proposals: repairProposals,
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
		build, buildErr := s.BuildClientExecutionPackage(ctx, state.ProjectID, defaultDesktopOrgID)
		if buildErr != nil || build.Package.ConfidenceSummary == nil || build.Package.ConfidenceSummary.Readiness == model.PackageReadinessBlocked {
			reason := assistantPackageBlockReason(build, buildErr)
			session.ActiveWorkstation = model.AssistantWorkstationEvidence
			session.WorkstationTitle = "执行包门禁需要处理"
			dismissAssistantProposalsOfKinds(session, proposal.ID, model.AssistantProposalConfirmConfiguration, model.AssistantProposalStartLocalAnalysis)
			s.emitAssistantProgress(input.ProjectID, orchestrator.ProgressEvent{Level: orchestrator.ProgressLevelError, Message: "正式执行包门禁未通过", Detail: reason, ElapsedMS: time.Since(analysisStarted).Milliseconds()})
			proposal.ExecutionResult = map[string]any{"projectID": state.ProjectID, "analysisStarted": true, "analysisBlocked": true, "uploadApproved": false}
			return &model.AssistantMessage{
				ID: fmt.Sprintf("agent_package_blocked_%d", now.UnixNano()), Role: "agent", Kind: "error",
				Text: reason + "。本地分析结果已保留，但尚不能进入上传审批。", CreatedAt: now,
				TargetWorkstation: model.AssistantWorkstationEvidence,
				Proposals: []model.AssistantProposal{
					newAssistantProposal(model.AssistantProposalRetryPageScan, "重新扫描页面", "重新运行登录和业务页面预扫描，收集真实 Browser Scan 证据。", session.Configuration.Version, now, nil, model.AssistantWorkstationEvidence),
					newAssistantProposal(model.AssistantProposalRegenerateExecutionPackage, "重新生成执行包", "从权威状态重新生成全部执行层并再次运行正式包门禁。", session.Configuration.Version, now, nil, model.AssistantWorkstationRepair),
				},
			}, nil
		}
		s.emitAssistantProgress(input.ProjectID, orchestrator.ProgressEvent{Level: orchestrator.ProgressLevelSuccess, Message: "本地执行包草稿已生成", Detail: "正式包门禁已通过，等待独立上传审批。", ElapsedMS: time.Since(analysisStarted).Milliseconds()})
		session.ActiveWorkstation = model.AssistantWorkstationApproval
		session.WorkstationTitle = "执行包审批"
		dismissAssistantProposalsOfKinds(session, proposal.ID, model.AssistantProposalConfirmConfiguration, model.AssistantProposalStartLocalAnalysis)
		proposal.ExecutionResult = map[string]any{"projectID": state.ProjectID, "analysisStarted": true, "uploadApproved": false}
		readyText := "本地代码、网页证据和用户需求已完成分析，三合一执行包草稿已生成。请在右侧检查方案与风险；上传仍需独立人工审批。"
		if assistantRuntimeEvidencePending(build.Package.ConfidenceSummary) {
			readyText = "本地代码和用户需求已生成受限 Browser Agent 正式包；登录后页面、selector 与实际结果将在云端运行时采集并逐项验证。当前只表示执行合同可审批，不表示业务已执行成功。"
		}
		return &model.AssistantMessage{
			ID: fmt.Sprintf("agent_analysis_ready_%d", now.UnixNano()), Role: "agent", Kind: "status",
			Text:      readyText,
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
	case model.AssistantProposalRetryPageScan, model.AssistantProposalRegenerateExecutionPackage:
		projectID := firstNonEmptyString(session.Context.ProjectID, session.Configuration.AnalysisProjectID)
		if projectID == "" {
			return nil, errors.New("analysis project is missing")
		}
		rerunInput, regenerateErr := s.userInputFromConfiguration(session.Configuration)
		if regenerateErr == nil {
			rerunInput.ProjectID = projectID
		}
		var state *orchestrator.CascadeState
		if regenerateErr == nil {
			state, regenerateErr = s.GenerateExecutionPackage(ctx, rerunInput)
		}
		if regenerateErr != nil {
			return nil, regenerateErr
		}
		session.Configuration.AnalysisProjectID = state.ProjectID
		session.Context.ProjectID = state.ProjectID
		build, buildErr := s.BuildClientExecutionPackage(ctx, state.ProjectID, defaultDesktopOrgID)
		if buildErr != nil || build.Package.ConfidenceSummary == nil || build.Package.ConfidenceSummary.Readiness == model.PackageReadinessBlocked {
			reason := assistantPackageBlockReason(build, buildErr)
			session.ActiveWorkstation = model.AssistantWorkstationEvidence
			session.WorkstationTitle = "执行包门禁需要处理"
			proposal.ExecutionResult = map[string]any{"projectID": state.ProjectID, "regenerated": true, "analysisBlocked": true, "uploadApproved": false}
			return &model.AssistantMessage{ID: fmt.Sprintf("agent_repair_blocked_%d", time.Now().UnixNano()), Role: "agent", Kind: "error", Text: reason + "。重新生成已完成，但正式包仍被阻断。", CreatedAt: time.Now().UTC(), TargetWorkstation: model.AssistantWorkstationEvidence, Proposals: []model.AssistantProposal{
				newAssistantProposal(model.AssistantProposalRetryPageScan, "重新扫描页面", "重新运行登录和业务页面预扫描，收集真实 Browser Scan 证据。", session.Configuration.Version, time.Now().UTC(), nil, model.AssistantWorkstationEvidence),
			}}, nil
		}
		session.ActiveWorkstation = model.AssistantWorkstationApproval
		session.WorkstationTitle = "执行包审批"
		dismissAssistantProposalsOfKinds(session, proposal.ID, model.AssistantProposalRetryPageScan, model.AssistantProposalRegenerateExecutionPackage)
		proposal.ExecutionResult = map[string]any{"projectID": state.ProjectID, "regenerated": true, "uploadApproved": false}
		readyText := "页面证据和执行包已重新生成，请检查审批摘要。"
		if assistantRuntimeEvidencePending(build.Package.ConfidenceSummary) {
			readyText = "受限 Browser Agent 执行合同已重新生成；登录后页面、selector 与实际结果仍由运行时采集验证，请检查审批摘要。"
		}
		return &model.AssistantMessage{ID: fmt.Sprintf("agent_repaired_%d", time.Now().UnixNano()), Role: "agent", Kind: "status", Text: readyText, CreatedAt: time.Now().UTC(), TargetWorkstation: model.AssistantWorkstationApproval}, nil
	case model.AssistantProposalSelectProjectSource, model.AssistantProposalSelectLocalProject, model.AssistantProposalConnectGitHub,
		model.AssistantProposalAttachRequirementDocument, model.AssistantProposalAttachBrandAsset,
		model.AssistantProposalStoreDemoCredential:
		proposal.ExecutionResult = map[string]any{"clientActionRequired": true, "action": proposal.Kind}
	default:
		return nil, errors.New("unsupported assistant proposal kind")
	}
	return nil, nil
}

func assistantPackageBlockReason(build ClientExecutionPackageBuild, err error) string {
	if err != nil {
		return "正式执行包预检未通过：" + bridgeErrorInfo(err).Message
	}
	if build.Package.ConfidenceSummary != nil && len(build.Package.ConfidenceSummary.BlockingReasons) > 0 {
		return "正式执行包确信度被阻断：" + strings.Join(build.Package.ConfidenceSummary.BlockingReasons, "；")
	}
	return "正式执行包缺少可验证的确信度结果"
}

func assistantRuntimeEvidencePending(summary *model.PackageConfidenceSummary) bool {
	return summary != nil && summary.Readiness != model.PackageReadinessBlocked && summary.ExecutionContractCoverage == 1 && summary.RuntimePageEvidenceCoverage < 1
}

func invalidateAssistantAnalysisContext(session *model.AssistantSession) {
	if session == nil {
		return
	}
	session.Configuration.AnalysisProjectID = ""
	session.ActiveWorkstation = model.AssistantWorkstationOverview
	session.WorkstationTitle = assistantWorkstationTitle(model.AssistantWorkstationOverview)
	session.WorkstationStatus = "Configuration 已变更，等待重新分析"
}

func configurationFollowUpMessage(next model.ProjectConfigurationDraft, now time.Time) *model.AssistantMessage {
	if next.Readiness == "ready" {
		return &model.AssistantMessage{
			ID: fmt.Sprintf("agent_ready_%d", now.UnixNano()), Role: "agent", Kind: "proposal",
			Text: "I have enough context to prepare this project. Would you like me to continue?", CreatedAt: now,
			TargetWorkstation: model.AssistantWorkstationOverview,
			Proposals:         []model.AssistantProposal{newAssistantProposal(model.AssistantProposalConfirmConfiguration, "Prepare this project", "Cascade will analyze the approved local context and prepare a reviewable execution draft. Nothing will be uploaded.", next.Version, now, nil, model.AssistantWorkstationApproval)},
		}
	}
	if question := nextAssistantQuestion(next); question != nil {
		return &model.AssistantMessage{
			ID: fmt.Sprintf("agent_question_%d", now.UnixNano()), Role: "agent", Kind: "answer",
			Text: question.Prompt, Question: question, CreatedAt: now,
			TargetWorkstation: model.AssistantWorkstationOverview,
		}
	}
	if containsAssistantString(next.MissingFields, "sources") {
		return &model.AssistantMessage{
			ID: fmt.Sprintf("agent_sources_%d", now.UnixNano()), Role: "agent", Kind: "proposal",
			Text: "Where should I learn about the product from? You can choose a local folder or a GitHub repository.", CreatedAt: now,
			TargetWorkstation: model.AssistantWorkstationOverview,
			Proposals:         []model.AssistantProposal{newAssistantProposal(model.AssistantProposalSelectProjectSource, "Choose a product source", "Select a local folder or GitHub repository. Cascade stores only a safe reference in the conversation.", next.Version, now, nil, model.AssistantWorkstationOverview)},
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
				PrimaryLabel: assistantProposalPrimaryLabel(*proposal, session.Context.Locale), ProposalID: proposal.ID,
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
		session.NextAction = model.AssistantNextAction{Kind: "review_local_draft", Title: assistantCopy(session.Context.Locale, "Review the local draft", "检查本地生成结果"), Description: assistantCopy(session.Context.Locale, "Review evidence, the recording plan, and risk controls. Nothing is uploaded automatically.", "查看证据、录制方案和风险；确认配置不会自动上传。"), PrimaryLabel: assistantCopy(session.Context.Locale, "Review result", "查看生成结果"), TargetWorkstation: target}
		return
	}
	if session.Configuration.Readiness == "ready" {
		session.PendingQuestion = nil
		session.NextAction = model.AssistantNextAction{Kind: "confirm_configuration", Title: assistantCopy(session.Context.Locale, "Confirm the brief and prepare a plan", "确认配置并生成方案"), Description: assistantCopy(session.Context.Locale, "Analyze the project locally and prepare a reviewable draft. Nothing is uploaded.", "在本机分析项目并生成三合一草稿，不会上传。"), PrimaryLabel: assistantCopy(session.Context.Locale, "Confirm and prepare", "确认并生成方案"), TargetWorkstation: model.AssistantWorkstationOverview, RequiresUserAction: true}
		return
	}
	if question := nextAssistantQuestionForLocale(session.Configuration, session.Context.Locale); question != nil {
		session.PendingQuestion = question
	}
	session.NextAction = model.AssistantNextAction{Kind: "provide_configuration", Title: assistantCopy(session.Context.Locale, "Complete the demo brief", "补全演示目标"), Description: assistantCopy(session.Context.Locale, "Tell Cascade the product, audience, and the workflow this demo must show.", "告诉 Cascade 产品地址、目标受众和必须展示的业务流程。"), PrimaryLabel: assistantCopy(session.Context.Locale, "Continue the conversation", "继续对话"), TargetWorkstation: model.AssistantWorkstationOverview, RequiresUserAction: true, MissingFields: append([]string(nil), session.Configuration.MissingFields...)}
}

func assistantProposalPrimaryLabel(proposal model.AssistantProposal, locale string) string {
	if proposal.Kind == model.AssistantProposalSelectLocalProject && proposal.ExecutionResult != nil && proposal.ExecutionResult["detectedSources"] != nil {
		return assistantCopy(locale, "Use this folder", "确认该目录")
	}
	switch proposal.Kind {
	case model.AssistantProposalConfigurationPatch:
		return assistantCopy(locale, "Use these changes", "应用字段变更")
	case model.AssistantProposalSelectProjectSource:
		return assistantCopy(locale, "Choose a source", "选择项目来源")
	case model.AssistantProposalSelectLocalProject:
		return assistantCopy(locale, "Choose a folder", "选择本地目录")
	case model.AssistantProposalConnectGitHub:
		return assistantCopy(locale, "Connect GitHub", "连接 GitHub")
	case model.AssistantProposalAttachRequirementDocument:
		return assistantCopy(locale, "Choose a document", "选择需求文档")
	case model.AssistantProposalAttachBrandAsset:
		return assistantCopy(locale, "Choose brand assets", "选择品牌素材")
	case model.AssistantProposalStoreDemoCredential:
		return assistantCopy(locale, "Save securely", "安全保存账号")
	case model.AssistantProposalConfirmConfiguration, model.AssistantProposalStartLocalAnalysis:
		return assistantCopy(locale, "Confirm and prepare", "确认并生成方案")
	case model.AssistantProposalContinueWithWebpageEvidence:
		return assistantCopy(locale, "Continue with page evidence", "仅使用网页证据继续")
	default:
		return assistantCopy(locale, "Continue", "继续")
	}
}

func dismissStaleAssistantProposals(session *model.AssistantSession, currentProposalID string, currentVersion int64) {
	for messageIndex := range session.Messages {
		for proposalIndex := range session.Messages[messageIndex].Proposals {
			proposal := &session.Messages[messageIndex].Proposals[proposalIndex]
			if proposal.ID != currentProposalID && proposal.Status == "available" && proposal.BaseVersion != currentVersion {
				if assistantProposalCanRebase(proposal.Kind) {
					proposal.BaseVersion = currentVersion
					proposal.ExecutionResult = map[string]any{"rebased": true, "currentConfigurationVersion": currentVersion}
					continue
				}
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
		input.DemoCredentialRef = ref
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
		current.MustShow = cleanStringsPreserveOrder(*patch.MustShow)
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
	if len(draft.Sources) == 0 {
		missing = append(missing, "sources")
	}
	if assistantIntentRequiresLogin(draft) && len(draft.CredentialRefs) == 0 && !assistantIntentHasManualLoginCheckpoint(draft) {
		missing = append(missing, "credentialRefs_or_manualLoginCheckpoint")
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
	turnAllowsQuestion := session.ActiveTurn == nil || session.ActiveTurn.Status == "completed" || session.ActiveTurn.Status == "cancelled"
	if turnAllowsQuestion && session.Configuration.Readiness != "ready" && latestAvailableAssistantProposal(session) == nil {
		session.PendingQuestion = nextAssistantQuestionForLocale(session.Configuration, session.Context.Locale)
		if session.PendingQuestion != nil && assistantQuestionNeedsTurn(session, session.PendingQuestion.Field) {
			now := time.Now().UTC()
			session.Messages = append(session.Messages, model.AssistantMessage{
				ID:   fmt.Sprintf("agent_question_%s_%d", session.PendingQuestion.Field, now.UnixNano()),
				Role: "agent", Kind: "answer", Text: session.PendingQuestion.Prompt,
				Question: session.PendingQuestion, CreatedAt: now,
			})
		}
	}
	refreshAssistantNextAction(session)
	refreshAssistantActionState(session)
	return session
}

func assistantQuestionNeedsTurn(session *model.AssistantSession, field string) bool {
	lastQuestion := -1
	lastUser := -1
	for index, message := range session.Messages {
		if message.Role == "user" {
			lastUser = index
		}
		if message.Question != nil && message.Question.Field == field {
			lastQuestion = index
		}
	}
	return lastQuestion < lastUser || lastQuestion == -1
}

func (s *Service) autoStartAssistantAnalysisIfReady(ctx context.Context, session *model.AssistantSession, now time.Time) (*model.AssistantMessage, error) {
	if session == nil || session.Context.CreateProjectOnFirstTurn || session.ActionMode != assistantActionModeV2 || session.Configuration.Readiness != "ready" || session.Configuration.Confirmed || session.Configuration.AnalysisProjectID != "" {
		return nil, nil
	}
	proposal := newAssistantProposal(model.AssistantProposalStartLocalAnalysis, "自动运行本地分析", "依赖已满足，开始读取本地证据并生成执行包草稿；不会上传。", session.Configuration.Version, now, nil, model.AssistantWorkstationEvidence)
	proposal.RequiresConfirmation = false
	session.Messages = append(session.Messages, model.AssistantMessage{
		ID: fmt.Sprintf("agent_auto_analysis_%d", now.UnixNano()), Role: "agent", Kind: "status",
		Text: "本地来源和登录要求已满足，正在自动生成执行方案。", CreatedAt: now,
		TargetWorkstation: model.AssistantWorkstationEvidence, Proposals: []model.AssistantProposal{proposal},
	})
	stored := &session.Messages[len(session.Messages)-1].Proposals[0]
	followUp, err := s.executeAssistantProposal(ctx, session, stored)
	if err != nil {
		return nil, err
	}
	stored.Status = "confirmed"
	return followUp, nil
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

func (s *Service) detectAssistantLocalSources(value string) []model.ConfigurationSourceRef {
	if s == nil {
		return nil
	}
	seen := map[string]bool{}
	result := []model.ConfigurationSourceRef{}
	for _, candidate := range windowsAbsolutePathPattern.FindAllString(value, -1) {
		candidate = strings.TrimRight(strings.TrimSpace(candidate), "'”’)]}）")
		registered, err := s.RegisterLocalSource("local_repository", candidate)
		if err != nil || seen[registered.Ref] {
			continue
		}
		seen[registered.Ref] = true
		result = append(result, model.ConfigurationSourceRef{Ref: registered.Ref, Kind: registered.Kind, Label: redactAssistantText(registered.Label)})
	}
	return result
}

func attachDetectedAssistantLocalSources(proposals []model.AssistantProposal, sources []model.ConfigurationSourceRef) {
	if len(sources) == 0 {
		return
	}
	for index := range proposals {
		if proposals[index].Kind != model.AssistantProposalSelectLocalProject {
			continue
		}
		if proposals[index].ExecutionResult == nil {
			proposals[index].ExecutionResult = map[string]any{}
		}
		proposals[index].ExecutionResult["detectedSources"] = append([]model.ConfigurationSourceRef(nil), sources...)
		proposals[index].Title = "确认本地项目：" + sources[0].Label
		proposals[index].Description = "已在本机识别该目录。确认后只把 opaque source ref 写入 configuration；也可稍后改用原生目录选择器。"
		return
	}
}

func firstHTTPURL(value string) string {
	candidate := strings.TrimRight(assistantHTTPURLPattern.FindString(value), ").!?！？]}")
	parsed, err := url.Parse(candidate)
	if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" {
		return candidate
	}
	if match := assistantBareDomainPattern.FindStringSubmatch(value); len(match) > 1 {
		candidate = "https://" + strings.TrimRight(match[1], ").!?！？]}")
		if parsed, parseErr := url.Parse(candidate); parseErr == nil && parsed.Hostname() != "" {
			return candidate
		}
	}
	return ""
}

func assistantProposalCanRebase(kind model.AssistantProposalKind) bool {
	switch kind {
	case model.AssistantProposalSelectProjectSource, model.AssistantProposalSelectLocalProject,
		model.AssistantProposalConnectGitHub, model.AssistantProposalAttachRequirementDocument,
		model.AssistantProposalAttachBrandAsset, model.AssistantProposalStoreDemoCredential:
		return true
	default:
		return false
	}
}

func splitAssistantIntentSteps(value string) []string {
	replacer := strings.NewReplacer("->", "\n", "→", "\n", "，然后", "\n", "然后", "\n", "并且", "\n", "最后", "\n", "，", "\n", ";", "\n", "；", "\n")
	parts := strings.FieldsFunc(replacer.Replace(value), func(r rune) bool { return r == '\n' })
	steps := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), "：:、.-")
		if part != "" {
			steps = append(steps, part)
		}
	}
	return cleanStringsPreserveOrder(steps)
}

func inferAssistantIntentSteps(message string) []string {
	marker := regexp.MustCompile(`(?:任务目标|演示目标|目标|需求)\s*[：:]?\s*`)
	location := marker.FindStringIndex(message)
	if location == nil {
		return nil
	}
	value := message[location[1]:]
	if index := strings.Index(value, "测试用账号"); index >= 0 {
		value = value[:index]
	}
	steps := splitAssistantIntentSteps(value)
	if len(steps) > 1 {
		return steps
	}
	return nil
}

func assistantMessageContainsTaskIntent(message string) bool {
	normalized := strings.ToLower(message)
	return firstHTTPURL(message) != "" || containsAnyAssistantText(normalized, "任务目标", "演示目标", "目标", "需求", "登录", "登陆", "新建项目", "创建项目")
}

func normalizeAssistantProjectName(value string) string {
	for _, delimiter := range []string{"并且", "然后", "随后", "等待", "，", ","} {
		if index := strings.Index(value, delimiter); index >= 0 {
			value = value[:index]
		}
	}
	return strings.Trim(strings.TrimSpace(value), "：:、.。\"'“”")
}

func cleanStringsPreserveOrder(values []string) []string {
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
	return result
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
