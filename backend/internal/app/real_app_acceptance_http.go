package app

import (
	"fmt"
	"net/http"
	"strings"
)

func (s *DevHTTPServer) handleRealAppExecutionAcceptance(w http.ResponseWriter, r *http.Request) {
	if err := s.requireLocalServerAcceptance(r); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	view, err := s.service.GetRealAppExecutionAcceptance(r.Context(), strings.TrimSpace(r.URL.Query().Get("org_id")))
	writeBridgeValue(w, view, err)
}

func (s *DevHTTPServer) handleRealAppExecutionAcceptanceItem(w http.ResponseWriter, r *http.Request) {
	if err := s.requireLocalServerAcceptance(r); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	view, err := s.service.GetRealAppExecutionAcceptanceItem(r.Context(), strings.TrimSpace(r.URL.Query().Get("org_id")), r.PathValue("id"))
	writeBridgeValue(w, view, err)
}

func (s *DevHTTPServer) handleRealAppExecutionAcceptanceRun(w http.ResponseWriter, r *http.Request) {
	if err := s.requireLocalServerAcceptance(r); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	status, err := s.service.RunRealAppExecutionAcceptance(r.Context(), strings.TrimSpace(r.URL.Query().Get("org_id")), r.PathValue("id"))
	writeBridgeValue(w, status, err)
}

func (s *DevHTTPServer) handleRealAppExecutionAcceptanceArtifact(w http.ResponseWriter, r *http.Request) {
	if err := s.requireLocalServerAcceptance(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))
	item, err := s.service.GetRealAppExecutionAcceptanceItem(r.Context(), orgID, r.PathValue("id"))
	if err != nil || item.Status.ResultPackageID == "" {
		http.Error(w, "result package is unavailable", http.StatusBadRequest)
		return
	}
	artifact, err := s.service.GetResultArtifactFile(r.Context(), orgID, item.Status.ResultPackageID, r.PathValue("artifact_id"))
	if err != nil {
		http.Error(w, "artifact is unavailable", http.StatusBadRequest)
		return
	}
	if artifact.MimeType != "" {
		w.Header().Set("Content-Type", artifact.MimeType)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeDownloadFileName(artifact.Path, artifact.Artifact)))
	http.ServeFile(w, r, artifact.Path)
}
