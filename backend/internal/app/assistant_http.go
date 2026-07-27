package app

import (
	"net/http"
	"strings"

	"cascade-demoops/backend/internal/model"
)

func (s *DevHTTPServer) handleCreateAssistantSession(w http.ResponseWriter, r *http.Request) {
	var request struct { Context model.AssistantContext `json:"context"` }
	if err := decodeJSON(r, &request); err != nil { writeBridgeValue(w, nil, err); return }
	session, err := s.service.CreateAssistantSession(r.Context(), request.Context)
	writeBridgeValue(w, session, err)
}

func (s *DevHTTPServer) handleAssistantSessionRoute(w http.ResponseWriter, r *http.Request) {
	sessionID, suffix, ok := splitAssistantSessionRoute(r.URL.Path)
	if !ok { http.NotFound(w, r); return }
	switch {
	case r.Method == http.MethodGet && suffix == "":
		session, err := s.service.GetAssistantSession(r.Context(), sessionID)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && suffix == "/turns":
		var request struct { Message string `json:"message"` }
		if err := decodeJSON(r, &request); err != nil { writeBridgeValue(w, nil, err); return }
		session, err := s.service.SubmitAssistantTurn(r.Context(), sessionID, request.Message)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodGet && suffix == "/events":
		events, err := s.service.ListAssistantEvents(r.Context(), sessionID, r.URL.Query().Get("after"))
		writeBridgeValue(w, events, err)
	case r.Method == http.MethodPost && suffix == "/cancel":
		session, err := s.service.CancelAssistantSession(r.Context(), sessionID)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && strings.HasPrefix(suffix, "/proposals/") && strings.HasSuffix(suffix, "/confirm"):
		proposalID := strings.TrimSuffix(strings.TrimPrefix(suffix, "/proposals/"), "/confirm")
		session, err := s.service.ConfirmAssistantProposal(r.Context(), sessionID, proposalID)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && strings.HasPrefix(suffix, "/proposals/") && strings.HasSuffix(suffix, "/dismiss"):
		proposalID := strings.TrimSuffix(strings.TrimPrefix(suffix, "/proposals/"), "/dismiss")
		session, err := s.service.DismissAssistantProposal(r.Context(), sessionID, proposalID)
		writeBridgeValue(w, session, err)
	default:
		http.NotFound(w, r)
	}
}

func splitAssistantSessionRoute(path string) (string, string, bool) {
	const prefix = "/v1/desktop/assistant/sessions/"
	if !strings.HasPrefix(path, prefix) { return "", "", false }
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if strings.TrimSpace(parts[0]) == "" { return "", "", false }
	if len(parts) == 1 { return parts[0], "", true }
	return parts[0], "/" + strings.TrimRight(parts[1], "/"), true
}
