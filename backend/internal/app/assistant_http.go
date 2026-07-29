package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"cascade-demoops/backend/internal/model"
)

func (s *DevHTTPServer) handleCreateAssistantSession(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Context model.AssistantContext `json:"context"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	session, err := s.service.CreateAssistantSession(r.Context(), request.Context)
	writeBridgeValue(w, session, err)
}

func (s *DevHTTPServer) handleAssistantSessionRoute(w http.ResponseWriter, r *http.Request) {
	sessionID, suffix, ok := splitAssistantSessionRoute(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.Method == http.MethodGet && suffix == "":
		session, err := s.service.GetAssistantSession(r.Context(), sessionID)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && suffix == "/turns":
		var request model.AssistantTurnRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		session, err := s.service.SubmitAssistantTurn(r.Context(), sessionID, request)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodGet && suffix == "/events":
		after := r.URL.Query().Get("after")
		if after == "" {
			after = r.Header.Get("Last-Event-ID")
		}
		events, err := s.service.ListAssistantEvents(r.Context(), sessionID, after)
		if err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			for _, event := range events {
				data, _ := json.Marshal(event)
				_, _ = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, data)
			}
			return
		}
		writeBridgeValue(w, events, nil)
	case r.Method == http.MethodPost && suffix == "/cancel":
		session, err := s.service.CancelAssistantSession(r.Context(), sessionID)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && strings.HasPrefix(suffix, "/proposals/") && strings.HasSuffix(suffix, "/confirm"):
		proposalID := strings.TrimSuffix(strings.TrimPrefix(suffix, "/proposals/"), "/confirm")
		var request model.AssistantProposalDecisionRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &request); err != nil {
				writeBridgeValue(w, nil, err)
				return
			}
		}
		session, err := s.service.ConfirmAssistantProposal(r.Context(), sessionID, proposalID, request)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && strings.HasPrefix(suffix, "/proposals/") && strings.HasSuffix(suffix, "/dismiss"):
		proposalID := strings.TrimSuffix(strings.TrimPrefix(suffix, "/proposals/"), "/dismiss")
		var request model.AssistantProposalDecisionRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &request); err != nil {
				writeBridgeValue(w, nil, err)
				return
			}
		}
		session, err := s.service.DismissAssistantProposal(r.Context(), sessionID, proposalID, request)
		writeBridgeValue(w, session, err)
	default:
		http.NotFound(w, r)
	}
}

func splitAssistantSessionRoute(path string) (string, string, bool) {
	const prefix = "/v1/desktop/assistant/sessions/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if strings.TrimSpace(parts[0]) == "" {
		return "", "", false
	}
	if len(parts) == 1 {
		return parts[0], "", true
	}
	return parts[0], "/" + strings.TrimRight(parts[1], "/"), true
}
