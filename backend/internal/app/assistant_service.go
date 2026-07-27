package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

var assistantSecretPattern = regexp.MustCompile(`(?i)(password|token|secret|api[_-]?key)\s*[:=]\s*[^\s,;]+`)

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
	label := "Projects"
	if request.Surface == model.AssistantSurfaceRepositories { label = "Repositories" }
	activeWorkstation := model.AssistantWorkstationOverview
	workstationTitle := "Project overview"
	if request.Surface == model.AssistantSurfaceProjects && strings.TrimSpace(request.ProjectID) != "" {
		activeWorkstation = model.AssistantWorkstationEditor
		workstationTitle = "Video editor"
	}
	session := &model.AssistantSession{
		ID: id,
		Context: request,
		Status: "waiting_for_user",
		ActiveWorkstation: activeWorkstation,
		WorkstationTitle: workstationTitle,
		WorkstationStatus: "Ready for direction",
		Messages: []model.AssistantMessage{{ID: "welcome", Role: "agent", Kind: "answer", Text: fmt.Sprintf("I’m looking at %s context. Tell me what you want to understand or change, and I’ll suggest the next safe step.", strings.ToLower(label)), CreatedAt: now}},
	}
	return session, s.assistantStore.Save(ctx, session)
}

func (s *Service) GetAssistantSession(ctx context.Context, sessionID string) (*model.AssistantSession, error) {
	session, err := s.assistantStore.Load(ctx, sessionID)
	if err != nil { return nil, err }
	return normalizeAssistantSession(session), nil
}

func (s *Service) SubmitAssistantTurn(ctx context.Context, sessionID, message string) (*model.AssistantSession, error) {
	message = redactAssistantText(strings.TrimSpace(message))
	if message == "" { return nil, errors.New("assistant message is required") }
	if len(message) > 4000 { return nil, errors.New("assistant message is too long") }
	session, err := s.assistantStore.Load(ctx, sessionID)
	if err != nil { return nil, err }
	session = normalizeAssistantSession(session)
	now := time.Now().UTC()
	session.Status = "thinking"
	session.Messages = append(session.Messages, model.AssistantMessage{ID: fmt.Sprintf("user_%d", now.UnixNano()), Role: "user", Kind: "answer", Text: message, CreatedAt: now})
	text, evidence, proposals, workstation := s.assistantResponse(ctx, session.Context, message)
	session.ActiveWorkstation = workstation
	session.WorkstationTitle = assistantWorkstationTitle(workstation)
	if len(proposals) > 0 { session.WorkstationStatus = "Awaiting confirmation" } else { session.WorkstationStatus = "Ready for the next question" }
	kind := "answer"
	if len(proposals) > 0 { kind = "proposal" }
	session.Messages = append(session.Messages, model.AssistantMessage{ID: fmt.Sprintf("agent_%d", now.UnixNano()+1), Role: "agent", Kind: kind, Text: redactAssistantText(text), CreatedAt: now, TargetWorkstation: workstation, Evidence: evidence, Proposals: proposals})
	if len(proposals) > 0 { session.Status = "awaiting_confirmation" } else { session.Status = "waiting_for_user" }
	event := model.AssistantEvent{ID: fmt.Sprintf("%d", now.UnixNano()), SessionID: session.ID, Type: "message", Text: redactAssistantText(text), CreatedAt: now}
	session.Events = append(session.Events, event)
	session.LastEventID = event.ID
	if err := s.assistantStore.Save(ctx, session); err != nil { return nil, err }
	return session, nil
}

func (s *Service) ListAssistantEvents(ctx context.Context, sessionID, after string) ([]model.AssistantEvent, error) {
	session, err := s.assistantStore.Load(ctx, sessionID)
	if err != nil { return nil, err }
	if after == "" { return session.Events, nil }
	result := make([]model.AssistantEvent, 0, len(session.Events))
	for _, event := range session.Events { if event.ID > after { result = append(result, event) } }
	return result, nil
}

func (s *Service) ConfirmAssistantProposal(ctx context.Context, sessionID, proposalID string) (*model.AssistantSession, error) {
	return s.updateAssistantProposal(ctx, sessionID, proposalID, "confirmed")
}

func (s *Service) DismissAssistantProposal(ctx context.Context, sessionID, proposalID string) (*model.AssistantSession, error) {
	return s.updateAssistantProposal(ctx, sessionID, proposalID, "dismissed")
}

func (s *Service) CancelAssistantSession(ctx context.Context, sessionID string) (*model.AssistantSession, error) {
	session, err := s.assistantStore.Load(ctx, sessionID)
	if err != nil { return nil, err }
	session.Status = "idle"
	if err := s.assistantStore.Save(ctx, session); err != nil { return nil, err }
	return session, nil
}

func (s *Service) updateAssistantProposal(ctx context.Context, sessionID, proposalID, status string) (*model.AssistantSession, error) {
	session, err := s.assistantStore.Load(ctx, sessionID)
	if err != nil { return nil, err }
	found := false
	for messageIndex := range session.Messages {
		for proposalIndex := range session.Messages[messageIndex].Proposals {
			proposal := &session.Messages[messageIndex].Proposals[proposalIndex]
			if proposal.ID == proposalID { proposal.Status = status; found = true }
			if proposal.ID == proposalID && status == "confirmed" && model.IsAssistantWorkstation(proposal.TargetWorkstation) {
				session.ActiveWorkstation = proposal.TargetWorkstation
				session.WorkstationTitle = assistantWorkstationTitle(proposal.TargetWorkstation)
				session.WorkstationStatus = "Ready"
			}
		}
	}
	if !found { return nil, errors.New("assistant proposal not found") }
	session.Status = "waiting_for_user"
	if err := s.assistantStore.Save(ctx, session); err != nil { return nil, err }
	return session, nil
}

func (s *Service) assistantResponse(ctx context.Context, contextView model.AssistantContext, message string) (string, []model.AssistantEvidence, []model.AssistantProposal, model.AssistantWorkstation) {
	if contextView.Surface == model.AssistantSurfaceProjects && contextView.ProjectID == "" {
		projects, err := s.ListProjects(ctx)
		if err != nil || len(projects) == 0 { return "There are no durable demo projects to inspect yet. Start from Home and describe the customer story.", nil, nil, model.AssistantWorkstationOverview }
		sort.SliceStable(projects, func(i, j int) bool { return projects[i].UpdatedAt.After(projects[j].UpdatedAt) })
		best := projects[0]
		lower := strings.ToLower(message)
		for _, project := range projects { if strings.Contains(lower, "onboarding") && strings.Contains(strings.ToLower(project.Name), "onboarding") { best = project; break } }
		return fmt.Sprintf("I found %d demo project%s. The closest current match is %q, which covers %s.", len(projects), pluralSuffix(len(projects)), best.Name, best.ProductURL), []model.AssistantEvidence{{ID: "projects", Label: "Project index", Source: "Durable project state", Summary: fmt.Sprintf("%d project summaries available", len(projects)), Confidence: 0.98}}, []model.AssistantProposal{{ID: "open_" + best.ID, Kind: "open_project", Title: "Open this project", Description: "Load the selected project and inspect its evidence-backed workflow.", TargetID: best.ID, TargetWorkstation: model.AssistantWorkstationOverview, ActionIntent: "view_workstation", RequiresConfirmation: true, Status: "available"}}, model.AssistantWorkstationOverview
	}
	state, err := s.LoadProject(ctx, contextView.ProjectID)
	if err != nil || state == nil || state.ProjectContext == nil { return "I can answer once a project context is available. Open a project or connect a repository first.", nil, nil, model.AssistantWorkstationOverview }
	project := state.ProjectContext
	evidence := []model.AssistantEvidence{{ID: "product", Label: "Product context", Source: project.ProductURL, Summary: "Product URL and project policy are available to the assistant.", Confidence: 0.9}}
	if project.Inputs != nil && len(project.Inputs.Repositories) > 0 { evidence = append(evidence, model.AssistantEvidence{ID: "repositories", Label: "Repository context", Source: "Connected read-only repositories", Summary: fmt.Sprintf("%d repository connection(s) are available.", len(project.Inputs.Repositories)), Confidence: 0.86}) }
	brief, briefErr := s.assistantDeps.RequirementReader.ReadRequirements(ctx, project)
	if briefErr == nil && brief != nil && brief.Objective != "" { evidence = append(evidence, model.AssistantEvidence{ID: "requirements", Label: "Requirement brief", Source: "RequirementReaderAgent", Summary: redactAssistantText(brief.Objective), Confidence: 0.84}) }
	if contextView.Surface == model.AssistantSurfaceRepositories {
		codeSnapshots, codeErr := s.assistantDeps.CodeReader.ReadCode(ctx, project, brief)
		if codeErr == nil { evidence = append(evidence, model.AssistantEvidence{ID: "code", Label: "Code understanding", Source: "CodeReaderAgent", Summary: fmt.Sprintf("Read-only analysis returned %d code snapshot(s).", len(codeSnapshots)), Confidence: 0.8}) }
		if strings.Contains(strings.ToLower(message), "understand") || strings.Contains(strings.ToLower(message), "learn") || strings.Contains(strings.ToLower(message), "analy") {
			if _, _, _, intelligenceErr := s.assistantDeps.ProjectIntelligence.RunProjectIntelligence(ctx, project, brief, codeSnapshots, state.PageSnapshots); intelligenceErr == nil { evidence = append(evidence, model.AssistantEvidence{ID: "intelligence", Label: "Project intelligence", Source: "ProjectIntelligenceGraph", Summary: "Repository and product context can be combined into a reviewable capability map.", Confidence: 0.78}) }
		}
		return "I can use this repository as read-only product evidence. I’ll surface routes, components, and likely demo surfaces, then ask before attaching or preparing anything.", evidence, []model.AssistantProposal{{ID: "prepare_" + state.ProjectID, Kind: "prepare_understanding", Title: "Prepare a repository understanding summary", Description: "Run the read-only code understanding path and return evidence for review.", TargetID: state.ProjectID, TargetWorkstation: model.AssistantWorkstationEvidence, ActionIntent: "prepare_understanding", RequiresConfirmation: true, Status: "available"}}, model.AssistantWorkstationEvidence
	}
	workstation := assistantWorkstationForPrompt(message)
	proposal := []model.AssistantProposal(nil)
	text := fmt.Sprintf("I have the context for %q and can reason from its product, requirements, and existing evidence.", project.Name)
	if workstation != model.AssistantWorkstationOverview {
		text = fmt.Sprintf("I moved the workstation to %s so we can work through the next useful layer of this project.", strings.ToLower(assistantWorkstationTitle(workstation)))
	}
	if workstation == model.AssistantWorkstationApproval || workstation == model.AssistantWorkstationExecution {
		proposal = []model.AssistantProposal{{ID: "prepare_" + state.ProjectID, Kind: "prepare_execution", Title: "Prepare the approval view", Description: "Bring the execution package and safety checklist into the workstation. Running remains separately approval-gated.", TargetID: state.ProjectID, TargetWorkstation: model.AssistantWorkstationApproval, ActionIntent: "prepare_execution", RequiresConfirmation: true, Status: "available"}}
	}
	return text, evidence, proposal, workstation
}

func assistantWorkstationForPrompt(message string) model.AssistantWorkstation {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "evidence"), strings.Contains(lower, "found"), strings.Contains(lower, "understand"), strings.Contains(lower, "learn"):
		return model.AssistantWorkstationEvidence
	case strings.Contains(lower, "plan"), strings.Contains(lower, "journey"), strings.Contains(lower, "outline"):
		return model.AssistantWorkstationPlan
	case strings.Contains(lower, "approve"), strings.Contains(lower, "approval"), strings.Contains(lower, "package"):
		return model.AssistantWorkstationApproval
	case strings.Contains(lower, "run"), strings.Contains(lower, "execute"), strings.Contains(lower, "record"):
		return model.AssistantWorkstationExecution
	case strings.Contains(lower, "repair"), strings.Contains(lower, "failed"), strings.Contains(lower, "failure"):
		return model.AssistantWorkstationRepair
	case strings.Contains(lower, "asset"), strings.Contains(lower, "video"), strings.Contains(lower, "screenshot"):
		return model.AssistantWorkstationAssets
	case strings.Contains(lower, "edit"), strings.Contains(lower, "timeline"):
		return model.AssistantWorkstationEditor
	default:
		return model.AssistantWorkstationOverview
	}
}

func assistantWorkstationTitle(workstation model.AssistantWorkstation) string {
	switch workstation {
	case model.AssistantWorkstationEvidence:
		return "Evidence"
	case model.AssistantWorkstationPlan:
		return "Demo plan"
	case model.AssistantWorkstationApproval:
		return "Approval"
	case model.AssistantWorkstationExecution:
		return "Execution"
	case model.AssistantWorkstationRepair:
		return "Repair"
	case model.AssistantWorkstationAssets:
		return "Generated assets"
	case model.AssistantWorkstationEditor:
		return "Editor"
	default:
		return "Project overview"
	}
}

func normalizeAssistantSession(session *model.AssistantSession) *model.AssistantSession {
	if session == nil { return session }
	if !model.IsAssistantWorkstation(session.ActiveWorkstation) {
		session.ActiveWorkstation = model.AssistantWorkstationOverview
	}
	if session.WorkstationTitle == "" {
		session.WorkstationTitle = assistantWorkstationTitle(session.ActiveWorkstation)
	}
	if session.WorkstationStatus == "" {
		session.WorkstationStatus = "Ready for direction"
	}
	return session
}

func assistantID(surface model.AssistantSurface, scope string) string { digest := sha256.Sum256([]byte(string(surface) + ":" + scope)); return "assistant_" + hex.EncodeToString(digest[:12]) }
func pluralSuffix(count int) string { if count == 1 { return "" }; return "s" }
func redactAssistantText(value string) string { return assistantSecretPattern.ReplaceAllString(value, "$1: [redacted]") }
