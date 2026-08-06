package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
)

const (
	devExchangeHTTPEnv       = "CASCADE_DEV_EXCHANGE_HTTP"
	devExchangeTokenEnv      = "CASCADE_DEV_EXCHANGE_TOKEN"
	devExchangeRemoteBindEnv = "CASCADE_DEV_ALLOW_REMOTE_BIND"
	devExchangeAutoRunEnv    = "CASCADE_EXCHANGE_AUTO_RUN"
	cascadeOrgIDHeader       = "X-Cascade-Org-ID"
)

type exchangeUploadHTTPBody struct {
	UploadID   string                               `json:"upload_id,omitempty"`
	Envelope   model.ExchangeEnvelope               `json:"envelope,omitempty"`
	PayloadRef model.EncryptedPayloadRef            `json:"payload_ref,omitempty"`
	Request    *model.ExecutionPackageUploadRequest `json:"request,omitempty"`
	Payload    model.ClientExecutionPackage         `json:"payload,omitempty"`
}

type exchangeHTTPError struct {
	Error exchangeHTTPErrorBody `json:"error"`
}

type exchangeHTTPErrorBody struct {
	Code    string                    `json:"code"`
	Message string                    `json:"message"`
	Details []exchangeHTTPErrorDetail `json:"details,omitempty"`
}

func (s *DevHTTPServer) registerExchangeBootstrapRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/cascade-exchange", s.handleExchangeBootstrap)
	mux.HandleFunc("GET /aigc/.well-known/cascade-exchange", s.handleExchangeBootstrap)
	mux.HandleFunc("POST /v1/app-installations/register", s.handleAppInstallationRegister)
	mux.HandleFunc("POST /aigc/v1/app-installations/register", s.handleAppInstallationRegister)
	mux.HandleFunc("POST /v1/app-installations/session", s.handleAppInstallationSession)
	mux.HandleFunc("POST /aigc/v1/app-installations/session", s.handleAppInstallationSession)
	mux.HandleFunc("POST /v1/app-installations/refresh", s.handleAppInstallationRefresh)
	mux.HandleFunc("POST /aigc/v1/app-installations/refresh", s.handleAppInstallationRefresh)
	mux.HandleFunc("POST /v1/app-installations/revoke", s.requireDevExchangeAuth(s.handleAppInstallationRevoke))
	mux.HandleFunc("POST /aigc/v1/app-installations/revoke", s.requireDevExchangeAuth(s.handleAppInstallationRevoke))
}

func (s *DevHTTPServer) registerControlPlaneBootstrapRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/cascade-exchange", s.handleExchangeBootstrap)
	mux.HandleFunc("POST /v1/app-installations/register", s.handleAppInstallationRegister)
	mux.HandleFunc("POST /v1/app-installations/session", s.handleAppInstallationSession)
	mux.HandleFunc("POST /v1/app-installations/refresh", s.handleAppInstallationRefresh)
	mux.HandleFunc("POST /v1/app-installations/revoke", s.requireInstallationSession(s.handleAppInstallationRevoke))
}

func (s *DevHTTPServer) registerDevExchangeRoutes(mux *http.ServeMux) {
	if !s.devExchangeHTTPEnabledForRuntime() {
		return
	}
	mux.HandleFunc("POST /v1/execution-packages/init", s.requireDevExchangeAuth(s.handleExecutionPackageInit))
	mux.HandleFunc("POST /v1/execution-packages/validate", s.requireDevExchangeAuth(s.handleExecutionPackageValidate))
	mux.HandleFunc("POST /v1/execution-packages", s.requireDevExchangeAuth(s.handleExecutionPackageUpload))
	mux.HandleFunc("GET /v1/execution-packages/{id}/status", s.requireDevExchangeAuth(s.handleExecutionPackageStatus))
	mux.HandleFunc("GET /v1/execution-packages/{id}/events", s.requireDevExchangeAuth(s.handleExecutionPackageEvents))
	mux.HandleFunc("GET /v1/result-packages/{id}", s.requireDevExchangeAuth(s.handleResultPackageGet))
	mux.HandleFunc("GET /v1/result-packages/{id}/deliverables/{artifact_id}", s.requireDevExchangeAuth(s.handleDevResultDeliverableDownload))
	mux.HandleFunc("POST /v1/result-packages/{id}/ack", s.requireDevExchangeAuth(s.handleResultPackageAck))
	mux.HandleFunc("POST /v1/result-packages/{id}/reviews", s.requireDevExchangeAuth(s.handleResultPackageReview))
	mux.HandleFunc("POST /v1/result-packages/{id}/revisions", s.requireDevExchangeAuth(s.handleResultPackageRevision))
	mux.HandleFunc("GET /v1/dev/execution-packages", s.requireDevExchangeAuth(s.handleDevExecutionPackageList))
	mux.HandleFunc("GET /v1/dev/result-packages", s.requireDevExchangeAuth(s.handleDevResultPackageList))
	mux.HandleFunc("POST /v1/dev/execution-packages/{id}/run", s.requireDevExchangeAuth(s.handleDevExecutionPackageRun))
	mux.HandleFunc("POST /v1/dev/execution-packages/{id}/cancel", s.requireDevExchangeAuth(s.handleDevExecutionPackageCancel))
	mux.HandleFunc("GET /v1/dev/execution-packages/{id}/debug", s.requireDevExchangeAuth(s.handleDevExecutionPackageDebug))
	mux.HandleFunc("GET /v1/dev/result-packages/{id}/deliverables/{artifact_id}", s.requireDevExchangeAuth(s.handleDevResultDeliverableDownload))
	mux.HandleFunc("POST /aigc/v1/execution-packages/init", s.requireDevExchangeAuth(s.handleExecutionPackageInit))
	mux.HandleFunc("POST /aigc/v1/execution-packages/validate", s.requireDevExchangeAuth(s.handleExecutionPackageValidate))
	mux.HandleFunc("POST /aigc/v1/execution-packages", s.requireDevExchangeAuth(s.handleExecutionPackageUpload))
	mux.HandleFunc("GET /aigc/v1/execution-packages/{id}/status", s.requireDevExchangeAuth(s.handleExecutionPackageStatus))
	mux.HandleFunc("GET /aigc/v1/execution-packages/{id}/events", s.requireDevExchangeAuth(s.handleExecutionPackageEvents))
	mux.HandleFunc("GET /aigc/v1/result-packages/{id}", s.requireDevExchangeAuth(s.handleResultPackageGet))
	mux.HandleFunc("GET /aigc/v1/result-packages/{id}/deliverables/{artifact_id}", s.requireDevExchangeAuth(s.handleDevResultDeliverableDownload))
	mux.HandleFunc("POST /aigc/v1/result-packages/{id}/ack", s.requireDevExchangeAuth(s.handleResultPackageAck))
	mux.HandleFunc("POST /aigc/v1/result-packages/{id}/reviews", s.requireDevExchangeAuth(s.handleResultPackageReview))
	mux.HandleFunc("POST /aigc/v1/result-packages/{id}/revisions", s.requireDevExchangeAuth(s.handleResultPackageRevision))
	mux.HandleFunc("GET /aigc/v1/dev/execution-packages", s.requireDevExchangeAuth(s.handleDevExecutionPackageList))
	mux.HandleFunc("GET /aigc/v1/dev/result-packages", s.requireDevExchangeAuth(s.handleDevResultPackageList))
	mux.HandleFunc("POST /aigc/v1/dev/execution-packages/{id}/run", s.requireDevExchangeAuth(s.handleDevExecutionPackageRun))
	mux.HandleFunc("POST /aigc/v1/dev/execution-packages/{id}/cancel", s.requireDevExchangeAuth(s.handleDevExecutionPackageCancel))
	mux.HandleFunc("GET /aigc/v1/dev/execution-packages/{id}/debug", s.requireDevExchangeAuth(s.handleDevExecutionPackageDebug))
	mux.HandleFunc("GET /aigc/v1/dev/result-packages/{id}/deliverables/{artifact_id}", s.requireDevExchangeAuth(s.handleDevResultDeliverableDownload))
}

// ControlPlaneHandler exposes only the public App-to-Server exchange surface.
// Desktop bridge, editor, model settings, and dev diagnostics remain loopback-only.
func (s *DevHTTPServer) ControlPlaneHandler() http.Handler {
	mux := http.NewServeMux()
	s.registerControlPlaneBootstrapRoutes(mux)
	auth := s.requireInstallationSession
	mux.HandleFunc("POST /v1/execution-packages/init", auth(s.handleExecutionPackageInit))
	mux.HandleFunc("POST /v1/execution-packages/validate", auth(s.handleExecutionPackageValidate))
	mux.HandleFunc("POST /v1/execution-packages", auth(s.handleExecutionPackageUpload))
	mux.HandleFunc("GET /v1/execution-packages/{id}/status", auth(s.handleExecutionPackageStatus))
	mux.HandleFunc("GET /v1/execution-packages/{id}/events", auth(s.handleExecutionPackageEvents))
	mux.HandleFunc("GET /v1/result-packages/{id}", auth(s.handleResultPackageGet))
	mux.HandleFunc("GET /v1/result-packages/{id}/deliverables/{artifact_id}", auth(s.handleDevResultDeliverableDownload))
	mux.HandleFunc("POST /v1/result-packages/{id}/ack", auth(s.handleResultPackageAck))
	mux.HandleFunc("POST /v1/result-packages/{id}/reviews", auth(s.handleResultPackageReview))
	mux.HandleFunc("POST /v1/result-packages/{id}/revisions", auth(s.handleResultPackageRevision))
	return withControlPlaneHeaders(mux)
}

func (s *DevHTTPServer) devExchangeHTTPEnabledForRuntime() bool {
	if !devExchangeHTTPEnabled() {
		return false
	}
	return s.service == nil || s.service.runtime.Profile != config.ProfileDesktop
}

func (s *DevHTTPServer) handleExchangeBootstrap(w http.ResponseWriter, r *http.Request) {
	response, err := s.service.exchange.BootstrapDiscovery(r.Context(), exchangeBaseURLFromRequest(r), s.service.runtime.Environment)
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleAppInstallationRegister(w http.ResponseWriter, r *http.Request) {
	var request model.AppInstallationRegisterRequest
	if err := decodeJSON(r, &request); err != nil {
		writeExchangeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	response, err := s.service.exchange.RegisterInstallation(r.Context(), request)
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleAppInstallationSession(w http.ResponseWriter, r *http.Request) {
	var request model.AppInstallationSessionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeExchangeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	response, err := s.service.exchange.CreateInstallationSession(r.Context(), request)
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleAppInstallationRefresh(w http.ResponseWriter, r *http.Request) {
	response, err := s.service.exchange.RefreshInstallationSession(r.Context(), sessionTokenFromRequest(r))
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "invalid or expired") {
		writeExchangeError(w, http.StatusUnauthorized, "installation_session_required", err)
		return
	}
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleAppInstallationRevoke(w http.ResponseWriter, r *http.Request) {
	var request model.AppInstallationRevokeRequest
	if err := decodeJSON(r, &request); err != nil {
		writeExchangeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	err := s.service.exchange.RevokeInstallation(r.Context(), request)
	writeExchangeValue(w, map[string]bool{"revoked": err == nil}, err)
}

func (s *DevHTTPServer) handleExecutionPackageInit(w http.ResponseWriter, r *http.Request) {
	var request model.ExecutionPackageInitRequest
	if err := decodeJSON(r, &request); err != nil {
		writeExchangeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	response, err := s.service.InitExecutionPackage(r.Context(), request)
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleExecutionPackageUpload(w http.ResponseWriter, r *http.Request) {
	var body exchangeUploadHTTPBody
	if err := decodeJSON(r, &body); err != nil {
		writeExchangeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	request := model.ExecutionPackageUploadRequest{UploadID: body.UploadID, Envelope: body.Envelope, PayloadRef: body.PayloadRef}
	if body.Request != nil {
		request = *body.Request
	}
	if request.PayloadRef.Kind == "" {
		request.PayloadRef = request.Envelope.PayloadRef
	}
	response, err := s.service.UploadExecutionPackageFromHTTP(r.Context(), request, body.Payload, installationIDFromRequest(s.service.exchange, r))
	if err == nil && (s.autoRunExchange || devExchangeAutoRunEnabled()) && body.Payload.PackageID != "" && response.Status == model.ExchangePackageStatusAccepted {
		runStatus, runErr := s.service.RunUploadedExecutionPackage(r.Context(), responseOrgID(request.Envelope, body.Payload), response.ExchangePackageID)
		if runErr == nil && runStatus.ExchangePackageID != "" {
			response.Status = runStatus.Status
		}
	}
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleExecutionPackageValidate(w http.ResponseWriter, r *http.Request) {
	var body exchangeUploadHTTPBody
	if err := decodeJSON(r, &body); err != nil {
		writeExchangeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	request := model.ExecutionPackageUploadRequest{UploadID: body.UploadID, Envelope: body.Envelope, PayloadRef: body.PayloadRef}
	if body.Request != nil {
		request = *body.Request
	}
	if request.PayloadRef.Kind == "" {
		request.PayloadRef = request.Envelope.PayloadRef
	}
	response, err := s.service.ValidateExecutionPackage(r.Context(), request, body.Payload)
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleExecutionPackageStatus(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromRequest(r)
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "missing_org_id", err)
		return
	}
	response, err := s.service.GetExecutionPackageStatus(r.Context(), orgID, r.PathValue("id"))
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleExecutionPackageEvents(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromRequest(r)
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "missing_org_id", err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeExchangeError(w, http.StatusInternalServerError, "streaming_unsupported", errors.New("HTTP streaming is unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	nextIndex := eventIndexAfter(r.Header.Get("Last-Event-ID"), r.PathValue("id"))
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		status, statusErr := s.service.GetExecutionPackageStatus(r.Context(), orgID, r.PathValue("id"))
		if statusErr != nil {
			writeSSE(w, "error", "", map[string]string{"message": redactBridgeError(statusErr.Error())})
			flusher.Flush()
			return
		}
		for nextIndex < len(status.StageHistory) {
			event := status.StageHistory[nextIndex]
			if event.EventID == "" {
				event.EventID = fmt.Sprintf("%s:%d", status.ExchangePackageID, nextIndex+1)
			}
			writeSSE(w, "stage", event.EventID, event)
			nextIndex++
		}
		flusher.Flush()
		if status.Status == model.ExchangePackageStatusCompleted || status.Status == model.ExchangePackageStatusFailed || status.Status == model.ExchangePackageStatusCanceled || status.Status == model.ExchangePackageStatusExpired {
			writeSSE(w, "complete", "", status)
			flusher.Flush()
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func eventIndexAfter(lastEventID string, packageID string) int {
	prefix := strings.TrimSpace(packageID) + ":"
	if !strings.HasPrefix(strings.TrimSpace(lastEventID), prefix) {
		return 0
	}
	index, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(lastEventID), prefix))
	if err != nil || index < 0 {
		return 0
	}
	return index
}

func writeSSE(w http.ResponseWriter, eventType string, eventID string, value any) {
	data, _ := json.Marshal(value)
	if eventID != "" {
		_, _ = fmt.Fprintf(w, "id: %s\n", eventID)
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
}

func (s *DevHTTPServer) handleDevExecutionPackageList(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromRequest(r)
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "missing_org_id", err)
		return
	}
	response, err := s.service.ListExecutionPackages(r.Context(), orgID)
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleDevExecutionPackageRun(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromRequest(r)
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "missing_org_id", err)
		return
	}
	response, err := s.service.RunUploadedExecutionPackage(r.Context(), orgID, r.PathValue("id"))
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleDevExecutionPackageCancel(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromRequest(r)
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "missing_org_id", err)
		return
	}
	response, err := s.service.CancelExecutionPackage(r.Context(), orgID, r.PathValue("id"))
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleDevExecutionPackageDebug(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromRequest(r)
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "missing_org_id", err)
		return
	}
	response, err := s.service.GetExecutionPackageDebugView(r.Context(), orgID, r.PathValue("id"))
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleResultPackageGet(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromRequest(r)
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "missing_org_id", err)
		return
	}
	response, err := s.service.GetResultPackage(r.Context(), orgID, r.PathValue("id"))
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleDevResultPackageList(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromRequest(r)
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "missing_org_id", err)
		return
	}
	response, err := s.service.ListResultPackages(r.Context(), orgID)
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleDevResultDeliverableDownload(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromRequest(r)
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "missing_org_id", err)
		return
	}
	artifact, err := s.service.GetResultArtifactFile(r.Context(), orgID, r.PathValue("id"), r.PathValue("artifact_id"))
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "deliverable_unavailable", err)
		return
	}
	if artifact.MimeType != "" {
		w.Header().Set("Content-Type", artifact.MimeType)
	}
	if artifact.Artifact.ID != "" {
		w.Header().Set("X-Cascade-Artifact-ID", artifact.Artifact.ID)
	}
	if artifact.Artifact.Kind != "" {
		w.Header().Set("X-Cascade-Artifact-Kind", artifact.Artifact.Kind)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeDownloadFileName(artifact.Path, artifact.Artifact)))
	http.ServeFile(w, r, artifact.Path)
}

func (s *DevHTTPServer) handleResultPackageAck(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromRequest(r)
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "missing_org_id", err)
		return
	}
	var request model.ResultPackageAckRequest
	if err := decodeJSON(r, &request); err != nil {
		writeExchangeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	if request.ResultPackageID == "" {
		request.ResultPackageID = r.PathValue("id")
	}
	response, err := s.service.AcknowledgeResultPackage(r.Context(), orgID, request)
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleResultPackageReview(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromRequest(r)
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "missing_org_id", err)
		return
	}
	var request model.ResultReviewRequest
	if err := decodeJSON(r, &request); err != nil {
		writeExchangeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	response, err := s.service.ReviewResultPackage(r.Context(), orgID, r.PathValue("id"), request)
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) handleResultPackageRevision(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromRequest(r)
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, "missing_org_id", err)
		return
	}
	var request model.ResultRevisionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeExchangeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	response, err := s.service.RequestResultRevision(r.Context(), orgID, r.PathValue("id"), request)
	writeExchangeValue(w, response, err)
}

func (s *DevHTTPServer) requireDevExchangeAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token := sessionTokenFromRequest(r); token != "" {
			if _, ok := s.service.exchange.AuthenticateInstallationSession(token); ok {
				next(w, r)
				return
			}
		}
		token := strings.TrimSpace(os.Getenv(devExchangeTokenEnv))
		if token == "" || r.Header.Get("Authorization") != "Bearer "+token {
			writeExchangeError(w, http.StatusUnauthorized, "unauthorized", errors.New("invalid dev exchange bearer token"))
			return
		}
		next(w, r)
	}
}

func (s *DevHTTPServer) requireInstallationSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token := sessionTokenFromRequest(r); token != "" {
			if _, ok := s.service.exchange.AuthenticateInstallationSession(token); ok {
				next(w, r)
				return
			}
		}
		writeExchangeError(w, http.StatusUnauthorized, "installation_session_required", errors.New("a valid Cascade installation session is required"))
	}
}

func withControlPlaneHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func installationIDFromRequest(exchange *ExchangeIntakeService, r *http.Request) string {
	if exchange == nil {
		return ""
	}
	installation, ok := exchange.AuthenticateInstallationSession(sessionTokenFromRequest(r))
	if !ok || installation == nil {
		return ""
	}
	return installation.InstallID
}

func orgIDFromRequest(r *http.Request) (string, error) {
	orgID := strings.TrimSpace(r.Header.Get(cascadeOrgIDHeader))
	if orgID == "" {
		orgID = strings.TrimSpace(r.URL.Query().Get("org_id"))
	}
	if orgID == "" {
		return "", errors.New("org id is required via X-Cascade-Org-ID header or org_id query parameter")
	}
	return orgID, nil
}

func writeExchangeValue(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeExchangeError(w, http.StatusBadRequest, exchangeErrorCode(err, "exchange_error"), err)
		return
	}
	value = withDeliverableDownloadURLs(value)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

func withDeliverableDownloadURLs(value any) any {
	switch typed := value.(type) {
	case model.ExecutionPackageStatusResponse:
		addDeliverableDownloadURLs(typed.ResultSummary, typed.ResultPackageID)
		return typed
	case *model.ExecutionPackageStatusResponse:
		if typed != nil {
			addDeliverableDownloadURLs(typed.ResultSummary, typed.ResultPackageID)
		}
		return typed
	case ExecutionPackageDebugView:
		addDeliverableDownloadURLs(typed.Status.ResultSummary, typed.Status.ResultPackageID)
		addDeliverableDownloadURLs(typed.Result, typed.Status.ResultPackageID)
		return typed
	case *ExecutionPackageDebugView:
		if typed != nil {
			addDeliverableDownloadURLs(typed.Status.ResultSummary, typed.Status.ResultPackageID)
			addDeliverableDownloadURLs(typed.Result, typed.Status.ResultPackageID)
		}
		return typed
	case model.ExecutionPackageListResponse:
		addExecutionListDeliverableDownloadURLs(typed.Items)
		return typed
	case *model.ExecutionPackageListResponse:
		if typed != nil {
			addExecutionListDeliverableDownloadURLs(typed.Items)
		}
		return typed
	case model.ResultPackageListResponse:
		addResultListDeliverableDownloadURLs(typed.Items)
		return typed
	case *model.ResultPackageListResponse:
		if typed != nil {
			addResultListDeliverableDownloadURLs(typed.Items)
		}
		return typed
	default:
		return value
	}
}

func addExecutionListDeliverableDownloadURLs(items []model.ExecutionPackageListItem) {
	for index := range items {
		addDeliverableDownloadURLs(items[index].ResultSummary, items[index].ResultPackageID)
	}
}

func addResultListDeliverableDownloadURLs(items []model.ResultPackageListItem) {
	for index := range items {
		addDeliverableDownloadURLs(items[index].ResultSummary, items[index].ResultPackageID)
	}
}

func addDeliverableDownloadURLs(summary *model.ExecutionResultSummary, resultPackageID string) {
	if summary == nil || resultPackageID == "" {
		return
	}
	for index := range summary.Deliverables {
		if summary.Deliverables[index].ID == "" {
			continue
		}
		summary.Deliverables[index].DownloadURL = fmt.Sprintf(
			"/v1/result-packages/%s/deliverables/%s",
			urlPathEscape(resultPackageID),
			urlPathEscape(summary.Deliverables[index].ID),
		)
	}
}

func writeExchangeError(w http.ResponseWriter, status int, code string, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	message := ""
	if err != nil {
		message = redactBridgeError(err.Error())
	}
	_ = json.NewEncoder(w).Encode(exchangeHTTPError{Error: exchangeHTTPErrorBody{Code: code, Message: message, Details: exchangeErrorDetails(err)}})
}

func devExchangeHTTPEnabled() bool {
	return os.Getenv(devExchangeHTTPEnv) == "1"
}

func devExchangeRemoteBindAllowed() bool {
	return devExchangeHTTPEnabled() && os.Getenv(devExchangeRemoteBindEnv) == "1" && strings.TrimSpace(os.Getenv(devExchangeTokenEnv)) != ""
}

func devExchangeAutoRunEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(devExchangeAutoRunEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func exchangeBaseURLFromRequest(r *http.Request) string {
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	prefix := ""
	if strings.HasPrefix(r.URL.Path, "/aigc/") || r.URL.Path == "/aigc/.well-known/cascade-exchange" {
		prefix = "/aigc"
	} else if forwardedPrefix := strings.TrimRight(strings.TrimSpace(r.Header.Get("X-Forwarded-Prefix")), "/"); forwardedPrefix != "" {
		prefix = forwardedPrefix
	}
	return strings.TrimRight(scheme+"://"+host+prefix, "/")
}

func responseOrgID(envelope model.ExchangeEnvelope, payload model.ClientExecutionPackage) string {
	if envelope.OrgID != "" {
		return envelope.OrgID
	}
	return payload.OrgID
}

func safeDownloadFileName(path string, artifact model.ArtifactRef) string {
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || strings.TrimSpace(name) == "" {
		name = artifact.ID
	}
	if strings.TrimSpace(name) == "" {
		name = "deliverable"
	}
	replacer := strings.NewReplacer(`\`, "_", `/`, "_", `"`, "_", "\r", "_", "\n", "_")
	return replacer.Replace(name)
}

func urlPathEscape(value string) string {
	replacer := strings.NewReplacer("%", "%25", "/", "%2F", "?", "%3F", "#", "%23", " ", "%20")
	return replacer.Replace(value)
}
