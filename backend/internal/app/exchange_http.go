package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"cascade-demoops/backend/internal/model"
)

const (
	devExchangeHTTPEnv       = "CASCADE_DEV_EXCHANGE_HTTP"
	devExchangeTokenEnv      = "CASCADE_DEV_EXCHANGE_TOKEN"
	devExchangeRemoteBindEnv = "CASCADE_DEV_ALLOW_REMOTE_BIND"
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

func (s *DevHTTPServer) registerDevExchangeRoutes(mux *http.ServeMux) {
	if !devExchangeHTTPEnabled() {
		return
	}
	mux.HandleFunc("POST /v1/execution-packages/init", s.requireDevExchangeAuth(s.handleExecutionPackageInit))
	mux.HandleFunc("POST /v1/execution-packages", s.requireDevExchangeAuth(s.handleExecutionPackageUpload))
	mux.HandleFunc("GET /v1/execution-packages/{id}/status", s.requireDevExchangeAuth(s.handleExecutionPackageStatus))
	mux.HandleFunc("GET /v1/result-packages/{id}", s.requireDevExchangeAuth(s.handleResultPackageGet))
	mux.HandleFunc("POST /v1/result-packages/{id}/ack", s.requireDevExchangeAuth(s.handleResultPackageAck))
	mux.HandleFunc("GET /v1/dev/execution-packages", s.requireDevExchangeAuth(s.handleDevExecutionPackageList))
	mux.HandleFunc("GET /v1/dev/result-packages", s.requireDevExchangeAuth(s.handleDevResultPackageList))
	mux.HandleFunc("POST /v1/dev/execution-packages/{id}/run", s.requireDevExchangeAuth(s.handleDevExecutionPackageRun))
	mux.HandleFunc("POST /v1/dev/execution-packages/{id}/cancel", s.requireDevExchangeAuth(s.handleDevExecutionPackageCancel))
	mux.HandleFunc("GET /v1/dev/execution-packages/{id}/debug", s.requireDevExchangeAuth(s.handleDevExecutionPackageDebug))
	mux.HandleFunc("GET /v1/dev/result-packages/{id}/deliverables/{artifact_id}", s.requireDevExchangeAuth(s.handleDevResultDeliverableDownload))
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
	response, err := s.service.UploadExecutionPackage(r.Context(), request, body.Payload)
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

func (s *DevHTTPServer) requireDevExchangeAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(os.Getenv(devExchangeTokenEnv))
		if token == "" {
			writeExchangeError(w, http.StatusServiceUnavailable, "dev_exchange_token_missing", errors.New("dev exchange HTTP requires CASCADE_DEV_EXCHANGE_TOKEN"))
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			writeExchangeError(w, http.StatusUnauthorized, "unauthorized", errors.New("invalid dev exchange bearer token"))
			return
		}
		next(w, r)
	}
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
			"/v1/dev/result-packages/%s/deliverables/%s",
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
