package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/credentialstore"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

type DevHTTPServer struct {
	service               *Service
	events                *devEventStore
	autoRunExchange       bool
	storeGitHubToken      func(string) error
	storeDemoCredential   func(string, string, string) error
	githubTokenConfigured func() bool
	deleteGitHubToken     func() error
}

type githubCredentialRequest struct {
	Token string `json:"token"`
}

type demoCredentialRequest struct {
	Ref      string `json:"ref"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type devLocalSourceRequest struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	DevTestAck bool   `json:"dev_test_ack"`
}

type ExecutionPackageRequest struct {
	UserInput *orchestrator.UserInput `json:"user_input,omitempty"`
}

type ClientExecutionPackageRequest struct {
	OrgID string `json:"org_id,omitempty"`
}

type CloudUploadRequest struct {
	OrgID    string                       `json:"org_id,omitempty"`
	UploadID string                       `json:"upload_id"`
	Build    *ClientExecutionPackageBuild `json:"build,omitempty"`
}

type CloudResultAckRequest struct {
	OrgID             string   `json:"org_id,omitempty"`
	ResultPackageID   string   `json:"result_package_id,omitempty"`
	ExchangePackageID string   `json:"exchange_package_id,omitempty"`
	ReceivedAssetIDs  []string `json:"received_asset_ids,omitempty"`
	VerifiedChecksums bool     `json:"verified_checksums"`
}

type DevExecutionEvent struct {
	ID        int64                      `json:"id"`
	ProjectID string                     `json:"project_id"`
	Level     orchestrator.ProgressLevel `json:"level"`
	Node      orchestrator.NodeName      `json:"node,omitempty"`
	Message   string                     `json:"message"`
	Detail    string                     `json:"detail,omitempty"`
	ElapsedMS int64                      `json:"elapsed_ms,omitempty"`
	CreatedAt time.Time                  `json:"created_at"`
}

func NewDevHTTPServer(service *Service) *DevHTTPServer {
	server := &DevHTTPServer{
		service:               service,
		events:                newDevEventStore(),
		storeGitHubToken:      credentialstore.StoreGitHubToken,
		storeDemoCredential:   credentialstore.StoreDemoCredential,
		githubTokenConfigured: credentialstore.GitHubTokenConfigured,
		deleteGitHubToken:     credentialstore.DeleteGitHubToken,
	}
	if service != nil {
		service.SetAssistantProgressSink(server.emitProjectEvent)
	}
	return server
}

func NewControlPlaneHTTPServer(service *Service) *DevHTTPServer {
	server := NewDevHTTPServer(service)
	server.autoRunExchange = true
	return server
}

func (s *DevHTTPServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/desktop/runtime-health", s.handleRuntimeHealth)
	mux.HandleFunc("PUT /v1/desktop/browser-agent-direct", s.handleDirectTransportSettings)
	mux.HandleFunc("PUT /v1/desktop/control-plane", s.handleControlPlaneSettings)
	mux.HandleFunc("GET /v1/desktop/github-credential", s.handleGitHubCredentialStatus)
	mux.HandleFunc("POST /v1/desktop/github-credential", s.handleStoreGitHubCredential)
	mux.HandleFunc("DELETE /v1/desktop/github-credential", s.handleDeleteGitHubCredential)
	mux.HandleFunc("POST /v1/desktop/demo-credential", s.handleStoreDemoCredential)
	mux.HandleFunc("PUT /v1/desktop/planning-model", s.handlePlanningModelSettings)
	mux.HandleFunc("DELETE /v1/desktop/planning-model/{provider}", s.handleDeletePlanningModelSettings)
	mux.HandleFunc("POST /v1/desktop/planning-model/verify", s.handleVerifyPlanningModel)
	// Local browser-development fallback for the native Wails directory picker.
	// The registered path never leaves the loopback Dev Bridge response.
	mux.HandleFunc("POST /v1/desktop/dev/local-sources", s.handleDevRegisterLocalSource)
	mux.HandleFunc("GET /v1/desktop/update", s.handleDesktopUpdateStatus)
	mux.HandleFunc("POST /v1/desktop/update/check", s.handleDesktopUpdateCheck)
	mux.HandleFunc("POST /v1/desktop/update/apply", s.handleDesktopUpdateApply)
	mux.HandleFunc("GET /v1/desktop/model-diagnostics", s.handleModelDiagnostics)
	mux.HandleFunc("POST /v1/desktop/model-diagnostics", s.handleModelDiagnostics)
	mux.HandleFunc("GET /v1/desktop/projects", s.handleListProjects)
	mux.HandleFunc("POST /v1/desktop/projects", s.handleCreateProject)
	mux.HandleFunc("POST /v1/desktop/assistant/sessions", s.handleCreateAssistantSession)
	mux.HandleFunc("/v1/desktop/assistant/sessions/", s.handleAssistantSessionRoute)
	mux.HandleFunc("/v1/desktop/projects/", s.handleProjectRoute)
	mux.HandleFunc("GET /v1/desktop/browser-agent-acceptance", s.handleBrowserAgentAcceptance)
	mux.HandleFunc("POST /v1/desktop/browser-agent-acceptance/run", s.handleBrowserAgentAcceptance)
	mux.HandleFunc("GET /v1/desktop/browser-agent-business-acceptance", s.handleBrowserAgentBusinessAcceptance)
	mux.HandleFunc("POST /v1/desktop/browser-agent-business-acceptance/run", s.handleBrowserAgentBusinessAcceptance)
	// Local dev/test only. This is not an App, Exchange, or production runtime API.
	mux.HandleFunc("POST /v1/desktop/dev-visible-browser-agent/prepare", s.handleDevVisibleBrowserAgent)
	mux.HandleFunc("/v1/desktop/dev-visible-browser-agent/", s.handleDevVisibleBrowserAgent)
	mux.HandleFunc("GET /v1/editor/sessions", s.handleEditorSessions)
	mux.HandleFunc("POST /v1/editor/sessions", s.handleEditorSessions)
	mux.HandleFunc("POST /v1/editor/sessions/from-result-package", s.handleEditorSessionFromResultPackage)
	mux.HandleFunc("GET /v1/editor/style-templates", s.handleEditorStyleTemplates)
	mux.HandleFunc("/v1/editor/sessions/", s.handleEditorSessionRoute)
	s.registerExchangeBootstrapRoutes(mux)
	s.registerDevExchangeRoutes(mux)
	return withDevLogging(withDevCORS(mux))
}

func (s *DevHTTPServer) handleDevRegisterLocalSource(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.service == nil || s.service.runtime.Profile != config.ProfileDev || s.service.runtime.Environment != "development" {
		writeBridgeValue(w, nil, errors.New("browser local-source registration is available only in a local development runtime"))
		return
	}
	if !requestFromLoopback(r) {
		writeBridgeValue(w, nil, errors.New("browser local-source registration requires a loopback client"))
		return
	}
	var request devLocalSourceRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	if !request.DevTestAck {
		writeBridgeValue(w, nil, errors.New("dev_test_ack=true is required for browser local-source registration"))
		return
	}
	if request.Kind != "local_repository" {
		writeBridgeValue(w, nil, errors.New("browser local-source registration supports only local_repository"))
		return
	}
	ref, err := s.service.RegisterLocalSource(request.Kind, request.Path)
	writeBridgeValue(w, ref, err)
}

func requestFromLoopback(r *http.Request) bool {
	if r == nil {
		return false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (s *DevHTTPServer) handleEditorSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sessions, err := s.service.ListEditorSessions(r.Context())
		writeBridgeValue(w, sessions, err)
	case http.MethodPost:
		var request model.EditorCreateSessionRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &request); err != nil {
				writeBridgeValue(w, nil, err)
				return
			}
		}
		session, err := s.service.CreateEditorSession(r.Context(), request)
		writeBridgeValue(w, session, err)
	default:
		http.NotFound(w, r)
	}
}

func (s *DevHTTPServer) handleEditorSessionFromResultPackage(w http.ResponseWriter, r *http.Request) {
	var request model.EditorCreateFromResultPackageRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	session, err := s.service.CreateEditorSessionFromResultPackage(r.Context(), request)
	writeBridgeValue(w, session, err)
}

func (s *DevHTTPServer) handleEditorStyleTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.service.ListVideoStyleTemplates(r.Context())
	writeBridgeValue(w, templates, err)
}

func (s *DevHTTPServer) handleEditorSessionRoute(w http.ResponseWriter, r *http.Request) {
	sessionID, suffix, ok := splitEditorSessionRoute(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.Method == http.MethodGet && suffix == "":
		session, err := s.service.GetEditorSession(r.Context(), sessionID)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && suffix == "/assets":
		var request model.EditorImportAssetRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		session, err := s.service.ImportEditorAsset(r.Context(), sessionID, request)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && suffix == "/uploads":
		s.handleEditorUpload(w, r, sessionID)
	case r.Method == http.MethodPost && suffix == "/style-references/assets":
		var request model.EditorImportAssetRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		session, err := s.service.ImportEditorStyleReference(r.Context(), sessionID, request)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && suffix == "/style-references/uploads":
		s.handleEditorStyleReferenceUpload(w, r, sessionID)
	case r.Method == http.MethodPost && suffix == "/plan":
		var request model.EditorSavePlanRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		session, err := s.service.SaveEditorPlan(r.Context(), sessionID, request)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && suffix == "/presentation-candidates/review":
		var request model.EditorReviewPresentationCandidateRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		session, err := s.service.ReviewEditorPresentationCandidate(r.Context(), sessionID, request)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && suffix == "/style-drafts":
		var request model.EditorStyleDraftRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		draft, err := s.service.CreateEditorStyleDraft(r.Context(), sessionID, request)
		writeBridgeValue(w, draft, err)
	case r.Method == http.MethodGet && strings.HasPrefix(suffix, "/style-drafts/"):
		draftID := strings.TrimPrefix(suffix, "/style-drafts/")
		if strings.TrimSpace(draftID) == "" || strings.Contains(draftID, "/") {
			http.NotFound(w, r)
			return
		}
		draft, err := s.service.GetEditorStyleDraft(r.Context(), sessionID, draftID)
		writeBridgeValue(w, draft, err)
	case r.Method == http.MethodPost && strings.HasSuffix(suffix, "/apply") && strings.HasPrefix(suffix, "/style-drafts/"):
		draftID := strings.TrimSuffix(strings.TrimPrefix(suffix, "/style-drafts/"), "/apply")
		if strings.TrimSpace(draftID) == "" || strings.Contains(draftID, "/") {
			http.NotFound(w, r)
			return
		}
		var request model.EditorApplyStyleDraftRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		session, err := s.service.ApplyEditorStyleDraft(r.Context(), sessionID, draftID, request)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && suffix == "/validate":
		report, err := s.service.ValidateEditorPlan(r.Context(), sessionID)
		writeBridgeValue(w, report, err)
	case r.Method == http.MethodPost && suffix == "/preview":
		session, err := s.service.StartEditorRender(sessionID, true)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && suffix == "/render":
		session, err := s.service.StartEditorRender(sessionID, false)
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && suffix == "/preview/cancel":
		session, err := s.service.CancelEditorRender(sessionID, "preview")
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodPost && suffix == "/render/cancel":
		session, err := s.service.CancelEditorRender(sessionID, "final")
		writeBridgeValue(w, session, err)
	case r.Method == http.MethodGet && strings.HasPrefix(suffix, "/media/"):
		handleEditorMedia(w, r, s.service, sessionID, strings.TrimPrefix(suffix, "/media/"))
	default:
		http.NotFound(w, r)
	}
}

func (s *DevHTTPServer) handleEditorUpload(w http.ResponseWriter, r *http.Request, sessionID string) {
	s.handleEditorMultipartUpload(w, r, sessionID, s.service.ImportEditorUpload)
}

func (s *DevHTTPServer) handleEditorStyleReferenceUpload(w http.ResponseWriter, r *http.Request, sessionID string) {
	s.handleEditorMultipartUpload(w, r, sessionID, s.service.ImportEditorStyleReferenceUpload)
}

func (s *DevHTTPServer) handleEditorMultipartUpload(w http.ResponseWriter, r *http.Request, sessionID string, importer func(context.Context, string, string, io.Reader) (model.EditorSession, error)) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxEditorUploadBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeBridgeValue(w, nil, errors.New("editor upload requires multipart/form-data"))
		return
	}
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			writeBridgeValue(w, nil, errors.New("editor upload file field is required"))
			return
		}
		if nextErr != nil {
			writeBridgeValue(w, nil, nextErr)
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			_ = part.Close()
			continue
		}
		session, importErr := importer(r.Context(), sessionID, part.FileName(), part)
		_ = part.Close()
		writeBridgeValue(w, session, importErr)
		return
	}
}

func handleEditorMedia(w http.ResponseWriter, r *http.Request, service *Service, sessionID, mediaRef string) {
	kind := mediaRef
	assetID := ""
	if strings.HasPrefix(mediaRef, "assets/") {
		kind = "asset"
		assetID = strings.TrimPrefix(mediaRef, "assets/")
	}
	filePath, mimeType, err := service.EditorMediaPath(sessionID, kind, assetID)
	if err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	if mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	if kind == "final" && r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="cascade-demo.mp4"`)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, filePath)
}

func (s *DevHTTPServer) handleRuntimeHealth(w http.ResponseWriter, r *http.Request) {
	writeBridgeValue(w, s.service.RuntimeConfigView(r.Context()), nil)
}

func (s *DevHTTPServer) handleDirectTransportSettings(w http.ResponseWriter, r *http.Request) {
	var request DirectTransportSettingsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	view, err := s.service.SaveDirectTransportSettings(r.Context(), request)
	writeBridgeValue(w, view, err)
}

func (s *DevHTTPServer) handleControlPlaneSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request ControlPlaneSettingsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	err := s.service.SaveControlPlaneBaseURL(request.BaseURL)
	if err == nil {
		_, err = s.service.EnsureExchangeSession(r.Context(), defaultDesktopOrgID, "")
	}
	writeBridgeValue(w, NewRuntimeConfigView(s.service.RuntimeConfig(), s.service.ExchangeIdentityStatus(r.Context())), err)
}

func (s *DevHTTPServer) handleGitHubCredentialStatus(w http.ResponseWriter, _ *http.Request) {
	writeBridgeValue(w, map[string]bool{"configured": s.githubTokenConfigured()}, nil)
}

func (s *DevHTTPServer) handleStoreGitHubCredential(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request githubCredentialRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	err := s.storeGitHubToken(request.Token)
	writeBridgeValue(w, map[string]bool{"configured": err == nil}, err)
}

func (s *DevHTTPServer) handleDeleteGitHubCredential(w http.ResponseWriter, _ *http.Request) {
	err := s.deleteGitHubToken()
	writeBridgeValue(w, map[string]bool{"configured": false}, err)
}

func (s *DevHTTPServer) handleStoreDemoCredential(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var request demoCredentialRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	request.Ref = strings.TrimSpace(request.Ref)
	if request.Ref == "" || len(request.Ref) > 128 || strings.ContainsAny(request.Ref, `/\\`) {
		writeBridgeValue(w, nil, errors.New("invalid demo credential ref"))
		return
	}
	err := s.storeDemoCredential(request.Ref, request.Username, request.Password)
	writeBridgeValue(w, map[string]any{"secretRef": "credential://demo/" + request.Ref, "configured": err == nil}, err)
}

func (s *DevHTTPServer) handlePlanningModelSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var request ModelSettingsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	err := s.service.SavePlanningModelSettings(request)
	writeBridgeValue(w, NewRuntimeConfigView(s.service.RuntimeConfig(), s.service.ExchangeIdentityStatus(r.Context())), err)
}

func (s *DevHTTPServer) handleDeletePlanningModelSettings(w http.ResponseWriter, r *http.Request) {
	err := s.service.DeletePlanningModelSettings(r.PathValue("provider"))
	writeBridgeValue(w, NewRuntimeConfigView(s.service.RuntimeConfig(), s.service.ExchangeIdentityStatus(r.Context())), err)
}

func (s *DevHTTPServer) handleVerifyPlanningModel(w http.ResponseWriter, r *http.Request) {
	writeBridgeValue(w, s.service.DiagnosePlanningModel(r.Context()), nil)
}

func (s *DevHTTPServer) handleDesktopUpdateStatus(w http.ResponseWriter, _ *http.Request) {
	status, err := desktopUpdateConfiguration(s.service.RuntimeConfig())
	writeBridgeValue(w, status, err)
}

func (s *DevHTTPServer) handleDesktopUpdateCheck(w http.ResponseWriter, r *http.Request) {
	status, err := CheckDesktopUpdate(r.Context(), s.service.RuntimeConfig())
	writeBridgeValue(w, status, err)
}

func (s *DevHTTPServer) handleDesktopUpdateApply(w http.ResponseWriter, r *http.Request) {
	err := ApplyDesktopUpdate(r.Context(), s.service.RuntimeConfig())
	writeBridgeValue(w, map[string]bool{"started": err == nil}, err)
}

func (s *DevHTTPServer) handleModelDiagnostics(w http.ResponseWriter, r *http.Request) {
	writeBridgeValue(w, s.service.DiagnoseModels(r.Context()), nil)
}

func (s *DevHTTPServer) handleBrowserAgentAcceptance(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		view, err := s.service.RunBrowserAgentAcceptance(r.Context())
		writeBridgeValue(w, view, err)
		return
	}
	view, err := s.service.GetBrowserAgentAcceptance(r.Context())
	writeBridgeValue(w, view, err)
}

func (s *DevHTTPServer) handleBrowserAgentBusinessAcceptance(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		view, err := s.service.RunBrowserAgentBusinessAcceptance(r.Context())
		writeBridgeValue(w, view, err)
		return
	}
	view, err := s.service.GetBrowserAgentBusinessAcceptance(r.Context())
	writeBridgeValue(w, view, err)
}

func (s *DevHTTPServer) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var input orchestrator.UserInput
	if err := decodeJSON(r, &input); err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	state, err := s.service.CreateProject(r.Context(), input)
	writeBridgeValue(w, state, err)
}

func (s *DevHTTPServer) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.service.ListProjects(r.Context())
	writeBridgeValue(w, projects, err)
}

func (s *DevHTTPServer) handleProjectRoute(w http.ResponseWriter, r *http.Request) {
	projectID, suffix, ok := splitProjectRoute(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.Method == http.MethodGet && suffix == "":
		state, err := s.service.LoadProject(r.Context(), projectID)
		writeBridgeValue(w, state, err)
	case r.Method == http.MethodGet && suffix == "/source-binding":
		assessment, err := s.service.GetSourceBinding(r.Context(), projectID)
		writeBridgeValue(w, assessment, err)
	case r.Method == http.MethodPost && suffix == "/source-binding/decisions":
		var request SourceBindingDecisionRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		state, err := s.service.DecideSourceBinding(r.Context(), projectID, request)
		writeBridgeValue(w, state, err)
	case r.Method == http.MethodPost && suffix == "/archive":
		err := s.service.ArchiveProject(r.Context(), projectID)
		writeBridgeValue(w, map[string]bool{"archived": err == nil}, err)
	case r.Method == http.MethodDelete && suffix == "":
		err := s.service.DeleteProject(r.Context(), projectID)
		writeBridgeValue(w, map[string]bool{"deleted": err == nil}, err)
	case r.Method == http.MethodPost && suffix == "/inputs":
		var inputs model.ProjectInputBundle
		if err := decodeJSON(r, &inputs); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		projectContext, err := s.service.SaveProjectInput(r.Context(), projectID, inputs)
		writeBridgeValue(w, projectContext, err)
	case r.Method == http.MethodPost && suffix == "/workflow-graph/revisions":
		var request GraphRevisionRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		result, err := s.service.ReviseWorkflowGraph(r.Context(), projectID, request)
		writeBridgeValue(w, result, err)
	case r.Method == http.MethodPost && suffix == "/execution-package":
		startedAt := time.Now()
		log.Printf("dev_bridge execution_package_start project_id=%s", projectID)
		s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
			Level:   orchestrator.ProgressLevelInfo,
			Message: "Dev Bridge 已收到执行包生成请求",
			Detail:  "本地后端开始调度 Agent 链路。",
		})
		var request ExecutionPackageRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &request); err != nil {
				log.Printf("dev_bridge execution_package_bad_request project_id=%s error=%s", projectID, err)
				s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
					Level:   orchestrator.ProgressLevelError,
					Message: "执行包请求解析失败",
					Detail:  err.Error(),
				})
				writeBridgeValue(w, nil, err)
				return
			}
		}
		ctx := orchestrator.WithProgressSink(r.Context(), func(event orchestrator.ProgressEvent) {
			s.emitProjectEvent(projectID, event)
		})
		var (
			state *orchestrator.CascadeState
			err   error
		)
		if request.UserInput != nil {
			state, err = s.service.GenerateExecutionPackage(ctx, *request.UserInput)
		} else {
			state, err = s.service.RegenerateExecutionPackage(ctx, projectID)
		}
		if err != nil {
			log.Printf("dev_bridge execution_package_error project_id=%s elapsed_ms=%d error=%s", projectID, time.Since(startedAt).Milliseconds(), err)
			s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
				Level:     orchestrator.ProgressLevelError,
				Message:   "执行包生成失败",
				Detail:    err.Error(),
				ElapsedMS: time.Since(startedAt).Milliseconds(),
			})
		} else if state != nil {
			s.events.CopyProjectEvents(projectID, state.ProjectID)
			log.Printf("dev_bridge execution_package_done project_id=%s generated_project_id=%s node=%s status=%s elapsed_ms=%d", projectID, state.ProjectID, state.CurrentNode, state.Status, time.Since(startedAt).Milliseconds())
			s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
				Level:     orchestrator.ProgressLevelSuccess,
				Node:      state.CurrentNode,
				Message:   "Dev Bridge 已返回执行包",
				Detail:    "前端可以展示中文思路文档、执行计划 JSON 和 TS 脚本。",
				ElapsedMS: time.Since(startedAt).Milliseconds(),
			})
			s.events.CopyProjectEvents(projectID, state.ProjectID)
		}
		writeBridgeValue(w, state, err)
	case r.Method == http.MethodGet && suffix == "/execution-package":
		state, err := s.service.LoadProject(r.Context(), projectID)
		writeBridgeValue(w, state, err)
	case r.Method == http.MethodPost && suffix == "/client-execution-package":
		var request ClientExecutionPackageRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &request); err != nil {
				writeBridgeValue(w, nil, err)
				return
			}
		}
		build, err := s.service.BuildClientExecutionPackage(r.Context(), projectID, request.OrgID)
		writeBridgeValue(w, build, err)
	case r.Method == http.MethodPost && suffix == "/browser-agent-direct/upload":
		var request DirectTransportUploadRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		result, err := s.service.UploadDirectExecutionPackage(r.Context(), projectID, request)
		writeBridgeValue(w, result, err)
	case r.Method == http.MethodGet && suffix == "/browser-agent-direct/status":
		status, err := s.service.GetDirectExecutionStatus(r.Context(), projectID, r.URL.Query().Get("job_id"))
		writeBridgeValue(w, status, err)
	case r.Method == http.MethodGet && suffix == "/browser-agent-direct/result":
		result, err := s.service.GetDirectResult(r.Context(), projectID, r.URL.Query().Get("job_id"))
		writeBridgeValue(w, result, err)
	case r.Method == http.MethodPost && suffix == "/browser-agent-direct/artifact/download":
		var request DirectArtifactDownloadRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		download, err := s.service.DownloadDirectArtifact(r.Context(), projectID, request)
		if err == nil && download.ChecksumVerified {
			download.MediaURL = "/v1/desktop/projects/" + url.PathEscape(projectID) + "/browser-agent-direct/artifact/media?job_id=" + url.QueryEscape(request.JobID) + "&file=" + url.QueryEscape(filepath.Base(download.LocalPath))
		}
		writeBridgeValue(w, download, err)
	case r.Method == http.MethodGet && suffix == "/browser-agent-direct/artifact/media":
		s.serveVerifiedDirectArtifact(w, r, projectID)
	case r.Method == http.MethodPost && suffix == "/browser-agent-direct/review":
		var request DirectResultReviewRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		review, err := s.service.ReviewDirectResult(r.Context(), projectID, request)
		writeBridgeValue(w, review, err)
	case r.Method == http.MethodPost && suffix == "/cloud/init":
		var request CloudUploadInitRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &request); err != nil {
				writeBridgeValue(w, nil, err)
				return
			}
		}
		orgID := firstNonEmptyString(request.OrgID, defaultDesktopOrgID)
		targetProjectID := firstNonEmptyString(request.ProjectID, projectID)
		build, session, err := s.service.ApproveCloudClientExecutionPackage(r.Context(), targetProjectID, orgID, request)
		if err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		initResponse, err := s.service.InitCloudExecutionPackageUpload(r.Context(), build)
		if err == nil {
			err = s.service.persistCloudInit(r.Context(), targetProjectID, orgID, initResponse)
		}
		result := CloudUploadInitResult{Build: &build, Init: initResponse, CloudBase: session.BaseURL}
		writeBridgeValue(w, result, err)
	case r.Method == http.MethodPost && suffix == "/cloud/preflight":
		var request CloudUploadInitRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &request); err != nil {
				writeBridgeValue(w, nil, err)
				return
			}
		}
		orgID := firstNonEmptyString(request.OrgID, defaultDesktopOrgID)
		targetProjectID := firstNonEmptyString(request.ProjectID, projectID)
		build, _, err := s.service.ApproveCloudClientExecutionPackage(r.Context(), targetProjectID, orgID, request)
		if err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		result, err := s.service.PreflightCloudExecutionPackage(r.Context(), build)
		writeBridgeValue(w, result, err)
	case r.Method == http.MethodPost && suffix == "/product-run/prepare":
		startedAt := time.Now()
		var request CloudLifecycleRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &request); err != nil {
				writeBridgeValue(w, nil, err)
				return
			}
		}
		if request.ProjectID == "" {
			request.ProjectID = projectID
		}
		s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
			Level:   orchestrator.ProgressLevelInfo,
			Message: "开始本地产品实战准备",
			Detail:  "只运行本地理解、页面预扫描、执行图和脚本包生成；不会连接云端 exchange。",
		})
		ctx := orchestrator.WithProgressSink(r.Context(), func(event orchestrator.ProgressEvent) {
			s.emitProjectEvent(projectID, event)
		})
		result, err := s.service.PrepareProductRun(ctx, request)
		if err != nil {
			s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
				Level:     orchestrator.ProgressLevelError,
				Message:   "本地产品实战准备失败",
				Detail:    err.Error(),
				ElapsedMS: time.Since(startedAt).Milliseconds(),
			})
		} else {
			s.events.CopyProjectEvents(projectID, result.State.ProjectID)
			s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
				Level:     orchestrator.ProgressLevelSuccess,
				Message:   "本地脚本包已准备完成",
				Detail:    "已生成三合一执行包预览；后续服务器连接和上传会单独执行。",
				ElapsedMS: time.Since(startedAt).Milliseconds(),
			})
		}
		writeBridgeValue(w, result, err)
	case r.Method == http.MethodPost && suffix == "/cloud/upload":
		var request CloudUploadRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		var build ClientExecutionPackageBuild
		var err error
		var session ExchangeSession
		if request.Build == nil {
			writeBridgeValue(w, nil, errors.New("approved execution package build is required"))
			return
		}
		build = *request.Build
		err = normalizeClientExecutionPackageForUpload(&build.Package)
		if err == nil && (build.BuildStatus != "approved" || build.Package.ApprovedAt.IsZero() || build.Package.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256 == "") {
			err = errors.New("draft execution package cannot be uploaded")
		}
		if err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		var sessionErr error
		if session.SessionToken == "" {
			session, sessionErr = s.service.EnsureExchangeSession(r.Context(), build.OrgID, build.ProjectID)
		}
		if sessionErr == nil {
			build, err = s.service.signBuildWithExchangeSession(build, session)
			if err != nil {
				writeBridgeValue(w, nil, err)
				return
			}
		}
		upload, err := s.service.UploadBuiltCloudExecutionPackage(r.Context(), request.UploadID, build)
		if err == nil {
			err = s.service.persistCloudUpload(r.Context(), projectID, build.OrgID, request.UploadID, upload)
		}
		cloudBase := s.service.effectiveControlPlaneBaseURL()
		if sessionErr == nil {
			cloudBase = session.BaseURL
		}
		result := CloudUploadPackageResult{Build: &build, Upload: upload, CloudBase: cloudBase}
		writeBridgeValue(w, result, err)
	case r.Method == http.MethodGet && suffix == "/cloud/status":
		orgID := r.URL.Query().Get("org_id")
		status, err := s.service.GetCloudExecutionPackageStatus(r.Context(), CloudStatusRequest{
			OrgID:             orgID,
			ExchangePackageID: r.URL.Query().Get("exchange_package_id"),
		})
		if err == nil {
			err = s.service.persistCloudStatus(r.Context(), projectID, orgID, status)
		}
		writeBridgeValue(w, status, err)
	case r.Method == http.MethodGet && suffix == "/cloud/events":
		s.streamCloudExecutionPackageEvents(w, r, projectID)
	case r.Method == http.MethodGet && suffix == "/cloud/result":
		orgID := r.URL.Query().Get("org_id")
		result, err := s.service.GetCloudResultPackage(r.Context(), CloudResultRequest{
			OrgID:           orgID,
			ResultPackageID: r.URL.Query().Get("result_package_id"),
		})
		if err == nil {
			err = s.service.persistCloudResult(r.Context(), projectID, orgID, result)
		}
		writeBridgeValue(w, result, err)
	case r.Method == http.MethodPost && suffix == "/cloud/deliverable/download":
		var request CloudDeliverableDownloadRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		if strings.TrimSpace(request.OutputDirectory) == "" {
			request.OutputDirectory = filepath.Join(
				s.service.runtime.ArtifactRoot,
				"desktop",
				"downloads",
				safePathSegment(projectID),
				safePathSegment(request.ResultPackageID),
			)
		}
		download, err := s.service.DownloadCloudResultDeliverable(r.Context(), request)
		if err == nil && download.ChecksumVerified {
			download.MediaURL = "/v1/desktop/projects/" + url.PathEscape(projectID) + "/cloud/deliverable/media?result_package_id=" + url.QueryEscape(request.ResultPackageID) + "&file=" + url.QueryEscape(filepath.Base(download.LocalPath))
			err = s.service.persistCloudDownload(r.Context(), projectID, request.ResultPackageID, download)
		}
		writeBridgeValue(w, download, err)
	case r.Method == http.MethodGet && suffix == "/cloud/deliverable/media":
		s.serveVerifiedCloudDeliverable(w, r, projectID)
	case r.Method == http.MethodGet && suffix == "/cloud/editor-materialization":
		result, err := s.service.GetCloudEditorMaterialization(r.Context(), CloudEditorMaterializationRequest{
			OrgID: r.URL.Query().Get("org_id"), ResultPackageID: r.URL.Query().Get("result_package_id"),
		})
		writeBridgeValue(w, result, err)
	case r.Method == http.MethodPost && suffix == "/cloud/ack":
		var request CloudResultAckRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		ack, err := s.service.AckCloudResultPackage(r.Context(), CloudAckRequest{
			OrgID:             request.OrgID,
			ResultPackageID:   request.ResultPackageID,
			ExchangePackageID: request.ExchangePackageID,
			ReceivedAssetIDs:  request.ReceivedAssetIDs,
			VerifiedChecksums: request.VerifiedChecksums,
			AckedByInstallID:  defaultDesktopInstallID,
			UseResultSummary:  len(request.ReceivedAssetIDs) == 0,
		})
		if err == nil {
			err = s.service.persistCloudAck(r.Context(), projectID, ack)
		}
		writeBridgeValue(w, ack, err)
	case r.Method == http.MethodPost && suffix == "/cloud/review":
		var request CloudResultReviewRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		review, err := s.service.ReviewCloudResultPackage(r.Context(), request)
		if err == nil {
			err = s.service.persistCloudReview(r.Context(), projectID, review)
		}
		writeBridgeValue(w, review, err)
	case r.Method == http.MethodPost && suffix == "/cloud/revision":
		var request CloudResultRevisionRequest
		if err := decodeJSON(r, &request); err != nil {
			writeBridgeValue(w, nil, err)
			return
		}
		revision, err := s.service.RequestCloudResultRevision(r.Context(), request)
		if err == nil {
			err = s.service.persistCloudRevision(r.Context(), projectID, revision)
		}
		writeBridgeValue(w, revision, err)
	case r.Method == http.MethodPost && suffix == "/cloud-lifecycle":
		startedAt := time.Now()
		var request CloudLifecycleRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(r, &request); err != nil {
				writeBridgeValue(w, nil, err)
				return
			}
		}
		if request.ProjectID == "" {
			request.ProjectID = projectID
		}
		s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
			Level:   orchestrator.ProgressLevelInfo,
			Message: "开始产品实战自动流程",
			Detail:  "兼容旧接口：本地生成执行包后将自动上传服务器、轮询状态并获取结果包。",
		})
		ctx := orchestrator.WithProgressSink(r.Context(), func(event orchestrator.ProgressEvent) {
			s.emitProjectEvent(projectID, event)
		})
		result, err := s.service.RunCloudLifecycle(ctx, request)
		if err != nil {
			s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
				Level:     orchestrator.ProgressLevelError,
				Message:   "产品实战自动流程失败",
				Detail:    err.Error(),
				ElapsedMS: time.Since(startedAt).Milliseconds(),
			})
		} else {
			s.events.CopyProjectEvents(projectID, result.State.ProjectID)
			s.emitProjectEvent(projectID, orchestrator.ProgressEvent{
				Level:     orchestrator.ProgressLevelSuccess,
				Message:   "产品实战自动流程完成",
				Detail:    "服务器状态已进入终态，结果包和诊断信息已返回。",
				ElapsedMS: time.Since(startedAt).Milliseconds(),
			})
		}
		writeBridgeValue(w, result, err)
	case r.Method == http.MethodGet && suffix == "/execution-events":
		afterID, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		writeBridgeValue(w, s.events.List(projectID, afterID), nil)
	default:
		http.NotFound(w, r)
	}
}

func (s *DevHTTPServer) serveVerifiedDirectArtifact(w http.ResponseWriter, r *http.Request, projectID string) {
	jobID := strings.TrimSpace(r.URL.Query().Get("job_id"))
	fileName := strings.TrimSpace(r.URL.Query().Get("file"))
	if jobID == "" || fileName == "" || filepath.Base(fileName) != fileName || strings.HasSuffix(strings.ToLower(fileName), ".part") {
		writeBridgeValue(w, nil, errors.New("verified direct artifact media reference is invalid"))
		return
	}
	filePath := filepath.Join(s.service.runtime.ArtifactRoot, "desktop", "direct-downloads", safePathSegment(projectID), safePathSegment(jobID), fileName)
	if !pathWithinRoot(filePath, filepath.Join(s.service.runtime.ArtifactRoot, "desktop", "direct-downloads")) {
		writeBridgeValue(w, nil, errors.New("verified direct artifact path is invalid"))
		return
	}
	info, err := os.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() || !verifyDownloadedDeliverableMedia(filePath) {
		writeBridgeValue(w, nil, errors.New("verified direct artifact was not found"))
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, filePath)
}

func (s *DevHTTPServer) serveVerifiedCloudDeliverable(w http.ResponseWriter, r *http.Request, projectID string) {
	resultPackageID := strings.TrimSpace(r.URL.Query().Get("result_package_id"))
	fileName := strings.TrimSpace(r.URL.Query().Get("file"))
	if resultPackageID == "" || fileName == "" || filepath.Base(fileName) != fileName || strings.HasSuffix(strings.ToLower(fileName), ".part") {
		writeBridgeValue(w, nil, errors.New("verified deliverable media reference is invalid"))
		return
	}
	filePath := filepath.Join(
		s.service.runtime.ArtifactRoot,
		"desktop",
		"downloads",
		safePathSegment(projectID),
		safePathSegment(resultPackageID),
		fileName,
	)
	info, err := os.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		writeBridgeValue(w, nil, errors.New("verified deliverable media was not found"))
		return
	}
	extension := strings.ToLower(filepath.Ext(fileName))
	if extension != ".mp4" && extension != ".webm" {
		writeBridgeValue(w, nil, errors.New("deliverable is not a supported review video"))
		return
	}
	if !verifyDownloadedDeliverableMedia(filePath) {
		writeBridgeValue(w, nil, errors.New("deliverable checksum is not verified"))
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, filePath)
}

func (s *DevHTTPServer) streamCloudExecutionPackageEvents(w http.ResponseWriter, r *http.Request, projectID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeBridgeValue(w, nil, errors.New("HTTP streaming is unavailable"))
		return
	}
	packageID := r.URL.Query().Get("exchange_package_id")
	lastEventID := firstNonEmptyString(r.Header.Get("Last-Event-ID"), s.service.cloudEventCursor(r.Context(), projectID, packageID))
	response, err := s.service.OpenCloudExecutionPackageEventStream(r.Context(), CloudStatusRequest{
		OrgID:             r.URL.Query().Get("org_id"),
		ExchangePackageID: packageID,
	}, lastEventID)
	if err != nil {
		writeBridgeValue(w, nil, err)
		return
	}
	defer response.Body.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(&sseFlushWriter{
		writer: w, flusher: flusher, service: s.service, ctx: r.Context(), projectID: projectID, packageID: packageID,
	}, response.Body)
}

type sseFlushWriter struct {
	writer    io.Writer
	flusher   http.Flusher
	service   *Service
	ctx       context.Context
	projectID string
	packageID string
	partial   string
}

func (w *sseFlushWriter) Write(data []byte) (int, error) {
	written, err := w.writer.Write(data)
	w.flusher.Flush()
	w.partial += strings.ReplaceAll(string(data), "\r\n", "\n")
	for {
		lineEnd := strings.IndexByte(w.partial, '\n')
		if lineEnd < 0 {
			break
		}
		line := strings.TrimSpace(w.partial[:lineEnd])
		w.partial = w.partial[lineEnd+1:]
		if strings.HasPrefix(line, "id:") {
			_ = w.service.persistCloudEventCursor(w.ctx, w.projectID, w.packageID, strings.TrimSpace(strings.TrimPrefix(line, "id:")))
		}
	}
	return written, err
}

func (s *DevHTTPServer) emitProjectEvent(projectID string, event orchestrator.ProgressEvent) {
	if s.events == nil {
		return
	}
	s.events.Add(projectID, event)
}

func withDevLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		log.Printf("dev_bridge request method=%s path=%s status=%d elapsed_ms=%d", r.Method, r.URL.Path, recorder.status, time.Since(startedAt).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func writeBridgeValue(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	status := http.StatusOK
	response := BridgeResponse{OK: true}
	if err != nil {
		status = http.StatusBadRequest
		info := bridgeErrorInfo(err)
		response = BridgeResponse{OK: false, Error: info.Message, ErrorInfo: &info}
	} else if value != nil {
		data, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			status = http.StatusInternalServerError
			info := bridgeErrorInfo(marshalErr)
			response = BridgeResponse{OK: false, Error: info.Message, ErrorInfo: &info}
		} else {
			response.Data = data
		}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func splitProjectRoute(path string) (string, string, bool) {
	const prefix = "/v1/desktop/projects/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	projectID := strings.TrimSpace(parts[0])
	if projectID == "" {
		return "", "", false
	}
	if len(parts) == 1 {
		return projectID, "", true
	}
	return projectID, "/" + strings.TrimRight(parts[1], "/"), true
}

func splitEditorSessionRoute(path string) (string, string, bool) {
	const prefix = "/v1/editor/sessions/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	sessionID := strings.TrimSpace(parts[0])
	if sessionID == "" {
		return "", "", false
	}
	if len(parts) == 1 {
		return sessionID, "", true
	}
	return sessionID, "/" + strings.TrimRight(parts[1], "/"), true
}

func withDevCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if !isAllowedDevOrigin(origin) {
			http.Error(w, "dev bridge origin is not allowed", http.StatusForbidden)
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID, X-Cascade-Org-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isAllowedDevOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	return strings.HasPrefix(origin, "http://127.0.0.1:") ||
		strings.HasPrefix(origin, "http://localhost:") ||
		origin == "http://wails.localhost" ||
		origin == "https://wails.localhost"
}

func EnsureLocalDevAddress(addr string) error {
	if strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "localhost:") {
		return nil
	}
	if strings.HasPrefix(addr, ":") {
		if devExchangeRemoteBindAllowed() {
			return nil
		}
		return errors.New("dev bridge must bind to 127.0.0.1 or localhost")
	}
	if devExchangeRemoteBindAllowed() {
		return nil
	}
	return errors.New("dev bridge refuses non-local bind address")
}

func EnsureControlPlaneAddress(addr string) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return errors.New("control plane address must include an explicit loopback host and port")
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("control plane must bind to loopback behind the HTTPS reverse proxy")
	}
	return nil
}

var (
	windowsPathPattern = regexp.MustCompile(`[A-Za-z]:\\[^:\r\n"]+`)
	unixPathPattern    = regexp.MustCompile(`(^|[\s"'(])(/[^:\r\n"\s]+)+`)
)

func redactBridgeError(message string) string {
	message = windowsPathPattern.ReplaceAllString(message, "[local_path]")
	message = unixPathPattern.ReplaceAllString(message, "$1[local_path]")
	message = userFacingBridgeError(message)
	return message
}

func userFacingBridgeError(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "llm json parse failed"),
		strings.Contains(lower, "cannot unmarshal"),
		strings.Contains(lower, "invalid character"),
		strings.Contains(lower, "unexpected non-whitespace character after json"):
		return "模型返回的 JSON 结构不稳定，系统已记录诊断。请重试，或使用 CASCADE_LLM_MODE=auto 让产品流程在格式异常时安全降级。"
	case strings.Contains(message, "执行包没有真实业务动作"):
		return message
	default:
		return message
	}
}

type devEventStore struct {
	mu      sync.Mutex
	nextID  int64
	events  map[string][]DevExecutionEvent
	maxSize int
}

func newDevEventStore() *devEventStore {
	return &devEventStore{
		events:  map[string][]DevExecutionEvent{},
		maxSize: 200,
	}
}

func (s *devEventStore) Add(projectID string, event orchestrator.ProgressEvent) DevExecutionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	stored := DevExecutionEvent{
		ID:        s.nextID,
		ProjectID: projectID,
		Level:     event.Level,
		Node:      event.Node,
		Message:   event.Message,
		Detail:    redactBridgeError(event.Detail),
		ElapsedMS: event.ElapsedMS,
		CreatedAt: time.Now().UTC(),
	}
	if stored.Level == "" {
		stored.Level = orchestrator.ProgressLevelInfo
	}
	s.events[projectID] = append(s.events[projectID], stored)
	if len(s.events[projectID]) > s.maxSize {
		s.events[projectID] = append([]DevExecutionEvent{}, s.events[projectID][len(s.events[projectID])-s.maxSize:]...)
	}
	return stored
}

func (s *devEventStore) List(projectID string, afterID int64) []DevExecutionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	events := s.events[projectID]
	result := make([]DevExecutionEvent, 0, len(events))
	for _, event := range events {
		if event.ID > afterID {
			result = append(result, event)
		}
	}
	return result
}

func (s *devEventStore) CopyProjectEvents(fromProjectID string, toProjectID string) {
	if fromProjectID == "" || toProjectID == "" || fromProjectID == toProjectID {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	source := s.events[fromProjectID]
	if len(source) == 0 {
		return
	}
	existingIDs := map[int64]bool{}
	for _, event := range s.events[toProjectID] {
		existingIDs[event.ID] = true
	}
	for _, event := range source {
		if existingIDs[event.ID] {
			continue
		}
		copied := event
		copied.ProjectID = toProjectID
		s.events[toProjectID] = append(s.events[toProjectID], copied)
	}
	if len(s.events[toProjectID]) > s.maxSize {
		s.events[toProjectID] = append([]DevExecutionEvent{}, s.events[toProjectID][len(s.events[toProjectID])-s.maxSize:]...)
	}
}
